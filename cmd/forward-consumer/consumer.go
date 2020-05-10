package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/pkg/errors"
	forward "github.com/tckz/personal-forward"
	"go.opencensus.io/trace"
	"go.opencensus.io/trace/propagation"
)

type Consumer struct {
	Propagation    propagation.HTTPFormat
	Client         *http.Client
	TargetPatterns []TargetPattern
	MaxDumpBytes   uint64
}

type TargetPattern struct {
	Pattern *regexp.Regexp
	Target  *url.URL
}

func (c *Consumer) shouldDumpWithBody(header http.Header) bool {
	ct := header.Get("content-type")
	clText := header.Get("content-length")
	cl, cle := strconv.ParseUint(clText, 10, 64)
	return (strings.HasPrefix(ct, "text/") || strings.Contains(ct, "json")) &&
		(clText != "" && cle == nil && cl <= c.MaxDumpBytes)
}

func (c *Consumer) chooseTarget(path string) *url.URL {
	for _, e := range c.TargetPatterns {
		if e.Pattern.MatchString(path) {
			return e.Target
		}
	}

	return nil
}

func (c *Consumer) ForwardRequest(ctx context.Context, doc *firestore.DocumentSnapshot) (err error) {
	defer func() {
		if err != nil {
			_, e2 := doc.Ref.Update(ctx, []firestore.Update{
				{
					Path:      "response",
					FieldPath: nil,
					Value: map[string]interface{}{
						"time":  firestore.ServerTimestamp,
						"error": err.Error(),
					},
				},
			})
			if e2 != nil {
				err = errors.Wrapf(e2, "*** write error")
			}
		}
	}()

	method, _ := forward.AsString(doc.DataAt("request.httpInfo.method"))
	requestURI, _ := forward.AsString(doc.DataAt("request.httpInfo.requestURI"))
	header, _ := forward.AsHeader(doc.DataAt("request.header"))
	body, _ := forward.AsByte(doc.DataAt("request.body"))

	u, err := url.Parse(requestURI)
	if err != nil {
		return err
	}

	target := c.chooseTarget(u.Path)
	if target == nil {
		return fmt.Errorf("no target match for %s", u.Path)
	}

	var span *trace.Span
	if cx := header.Get(forward.CloudTraceContext); cx != "" {
		req, _ := http.NewRequest("GET", "http://localhost/dummy", nil)
		req.Header.Set(forward.CloudTraceContext, cx)
		if sc, ok := c.Propagation.SpanContextFromRequest(req); ok {
			ctx, span = trace.StartSpanWithRemoteParent(ctx, "ForwardRequest", sc)
			defer span.End()
		}
	}

	u.Path = path.Join(target.Path, u.Path)
	u.Host = target.Host
	u.Scheme = target.Scheme

	req, err := http.NewRequest(method, u.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req = req.WithContext(ctx)
	req.Header = header

	if *optDumpForward {
		if b, err := httputil.DumpRequestOut(req, c.shouldDumpWithBody(req.Header)); err == nil {
			fmt.Fprintln(os.Stderr, string(b))
		}
	}

	begin := time.Now()
	res, err := c.Client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		io.Copy(ioutil.Discard, res.Body)
		res.Body.Close()
	}()

	logger.Infof("url=%s, status=%d, dur=%s", req.URL.String(), res.StatusCode, time.Since(begin))

	if *optDumpForward {
		if b, err := httputil.DumpResponse(res, c.shouldDumpWithBody(res.Header)); err == nil {
			fmt.Fprintln(os.Stderr, string(b))
		}
	}

	b, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return errors.Wrapf(err, "*** ioutil.ReadAll: response")
	}

	val := map[string]interface{}{
		"time":       firestore.ServerTimestamp,
		"statusCode": res.StatusCode,
		"header":     res.Header,
		"chunks":     0,
	}

	var chunks [][]byte
	if uint(len(b)) <= *optChunkBytes {
		val["body"] = b
	} else {
		// split body to chunks
		sliceSize := uint(len(b))
		chunkSize := *optChunkBytes

		for i := uint(0); i < sliceSize; i += chunkSize {
			end := i + chunkSize
			if sliceSize < end {
				end = sliceSize
			}
			chunks = append(chunks, b[i:end])
		}

		val["chunks"] = len(chunks)
	}

	logger.Infof("responseSize=%d, chunks=%d", len(b), len(chunks))

	_, err = doc.Ref.Update(ctx, []firestore.Update{
		{
			Path:      "response",
			FieldPath: nil,
			Value:     val,
		},
	})
	if err != nil {
		return errors.Wrapf(err, "*** doc.Ref.Update response: ID=%s", doc.Ref.ID)
	}

	// append chunks
	if len(chunks) > 0 {
		col := doc.Ref.Collection("responseBodies")
		for i, e := range chunks {
			ref, _, err := col.Add(ctx, map[string]interface{}{
				"index": i,
				"chunk": e,
				"size":  len(e),
			})
			if err != nil {
				return errors.Wrapf(err, "*** Add chunk of response: index=%d", i)
			}
			logger.Infof("Chunk[%d/%d]: Path=%s, size=%d", i+1, len(chunks), ref.Path, len(e))
		}
	}

	return nil
}
