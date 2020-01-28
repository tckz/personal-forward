package main

import (
	"bytes"
	"context"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"path"
	"regexp"

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
}

type TargetPattern struct {
	Pattern *regexp.Regexp
	Target  *url.URL
}

func (c *Consumer) ChooseTarget(path string) *url.URL {
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

	target := c.ChooseTarget(u.Path)
	if target == nil {
		return errors.New("no target match")
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

	res, err := c.Client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		io.Copy(ioutil.Discard, res.Body)
		res.Body.Close()
	}()

	logger.Infof("url=%s, status=%d", req.URL.String(), res.StatusCode)

	b, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return err
	}

	_, err = doc.Ref.Update(ctx, []firestore.Update{
		{
			Path:      "response",
			FieldPath: nil,
			Value: map[string]interface{}{
				"time":       firestore.ServerTimestamp,
				"statusCode": res.StatusCode,
				"header":     res.Header,
				"body":       b,
			},
		},
	})
	if err != nil {
		return err
	}

	return nil
}
