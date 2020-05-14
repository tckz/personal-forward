package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"cloud.google.com/go/firestore"
	"contrib.go.opencensus.io/exporter/stackdriver"
	firebase "firebase.google.com/go"
	"github.com/alicebob/miniredis"
	"github.com/go-redis/redis"
	"github.com/joho/godotenv"
	forward "github.com/tckz/personal-forward"
	"go.opencensus.io/exporter/stackdriver/propagation"
	"go.opencensus.io/plugin/ochttp"
	octrace "go.opencensus.io/trace"
	"go.uber.org/zap"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
)

var myName string
var logger *zap.SugaredLogger
var version string

var (
	optJSONKey         = flag.String("json-key", os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"), "/path/to/servicekey.json")
	optWorkers         = flag.Int("workers", 8, "Number of goroutines to process request")
	optDump            = flag.Bool("dump", false, "Dump received request or not")
	optExpire          = flag.Duration("expire", time.Minute*2, "Ignore too old request")
	optEndPointName    = flag.String("endpoint-name", "", "Identity of endpoint")
	optWithoutCleaning = flag.Bool("without-cleaning", false, "Delete request documents that is expired")
	optForwardTimeout  = flag.Duration("forward-timeout", time.Second*30, "Timeout for forwarding http request")
	optPatterns        forward.StringArrayFlag
	optTargets         forward.StringArrayFlag
	optDumpForward     = flag.Bool("dump-forward", false, "Dump forward request and response")
	optShowVersion     = flag.Bool("version", false, "Show version")
	optMaxDumpBytes    = flag.Uint("max-dump-bytes", 4096, "Size condition for determine whether dump body of request/response or not.")
	optChunkBytes      = flag.Uint("chunk-bytes", 1024*900, "Size of max chunk size of response")
)

func init() {
	godotenv.Load()

	flag.Var(&optPatterns, "pattern", "Path pattern for target.")
	flag.Var(&optTargets, "target", "URL of forwarding target.")
	flag.Parse()

	myName = filepath.Base(os.Args[0])

	zl, err := forward.NewLogger()
	if err != nil {
		panic(err)
	}
	logger = zl.Sugar().With(zap.String("app", myName))
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			var err error
			if e, ok := r.(error); ok {
				err = e
			} else {
				err = fmt.Errorf("%v", e)
			}
			logger.With(zap.Stack("stack"), zap.Error(err)).Errorf("*** panic: %v", r)
			// keep panic
			panic(r)
		}
	}()

	if *optShowVersion {
		fmt.Fprintln(os.Stderr, version)
		return
	}
	logger.Infof("ver=%s, args=%s", version, os.Args)
	run()

	logger.Info("exit")

}

func run() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if len(optTargets) == 0 && len(optPatterns) == 0 {
		optPatterns = append(optPatterns, "**")
		optTargets = append(optTargets, "http://localhost:3010")
	}

	if len(optTargets) != len(optPatterns) {
		logger.Fatalf("*** Number of patterns and targets must be same.")
	}

	replaceWildCard := strings.NewReplacer("**", ".*", "*", "[^/]*")
	var targetPatterns []TargetPattern
	for i, e := range optPatterns {
		re := regexp.MustCompile("^" + replaceWildCard.Replace(e))
		target := optTargets[i]
		u, err := url.Parse(target)
		if err != nil {
			logger.Fatalf("*** url.Parse: %s, err=%v", target, err)
		}
		targetPatterns = append(targetPatterns, TargetPattern{
			Pattern: re,
			Target:  u,
		})
	}

	logger.Infof("Patterns: %v", targetPatterns)

	if *optEndPointName == "" {
		logger.Fatalf("*** --endpoint-name must be specified")
	}

	logger = logger.With(zap.String("endpoint", *optEndPointName))

	mr, err := miniredis.Run()
	if err != nil {
		logger.Fatalf("*** miniredis.Run: %v", err)
	}
	defer mr.Close()

	var opts []option.ClientOption
	if *optJSONKey != "" {
		opts = append(opts, option.WithCredentialsFile(*optJSONKey))
	}

	exporter, err := stackdriver.NewExporter(stackdriver.Options{})
	if err != nil {
		logger.Fatalf("*** stackdriver.NewExporter(): %v", err)
	}
	defer exporter.Flush()
	octrace.RegisterExporter(exporter)
	octrace.ApplyConfig(octrace.Config{DefaultSampler: octrace.AlwaysSample()})

	redisClient := redis.NewUniversalClient(&redis.UniversalOptions{
		Addrs:        []string{mr.Addr()},
		MaxRetries:   3,
		DialTimeout:  time.Second * 2,
		ReadTimeout:  time.Second * 2,
		WriteTimeout: time.Second * 2,
		PoolSize:     100,
		MinIdleConns: 100,
		PoolTimeout:  time.Second * 3,
	})
	defer redisClient.Close()

	consumer := &Consumer{
		TargetPatterns: targetPatterns,
		Propagation:    &propagation.HTTPFormat{},
		Client: &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Transport: &ochttp.Transport{
				Propagation:    &propagation.HTTPFormat{},
				NewClientTrace: ochttp.NewSpanAnnotatingClientTrace,
			},
		},
		MaxDumpBytes: uint64(*optMaxDumpBytes),
	}

	app, err := firebase.NewApp(ctx, nil, opts...)
	if err != nil {
		logger.Fatalf("*** firebase.NewApp: %v", err)
	}

	client, err := app.Firestore(ctx)
	if err != nil {
		logger.Fatalf("*** app.Firestore: %v", err)
	}
	defer client.Close()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	ch := make(chan *firestore.DocumentSnapshot, *optWorkers)
	wg := &sync.WaitGroup{}
	go func() {
		s := <-sigCh
		logger.Infof("Received signal: %v", s)
		cancel()
	}()

	for i := 0; i < *optWorkers; i++ {
		wg.Add(1)
		logger := logger.With(zap.Int("worker", i))
		go func() {
			defer wg.Done()

			ctx := forward.WithLogger(ctx, logger.Desugar())

			for doc := range ch {
				func() {
					ctx, cancel := context.WithTimeout(ctx, *optForwardTimeout)
					defer cancel()

					err := consumer.ForwardRequest(ctx, doc)
					if err != nil {
						logger.With(zap.Error(err)).Errorf("*** forwardRequest: %s", err)
					}
				}()
			}
		}()
	}

	logger.Infof("Listening endpoint=%s", *optEndPointName)
	it := client.Collection("endpoints").
		Doc(*optEndPointName).
		Collection("requests").
		Snapshots(ctx)
	defer it.Stop()

	const iso8601Format = "2006-01-02T15:04:05.000Z0700"
	for {
		snapshot, err := it.Next()
		if err != nil {
			if s, ok := err.(forward.GRPCStatusHolder); err == iterator.Done || ok && s.GRPCStatus().Code() == codes.Canceled {
				break
			}
			logger.Fatalf("*** it.Next: %v", err)
		}

		for i, e := range snapshot.Changes {
			created, _ := forward.AsTime(e.Doc.DataAt("created"))
			uri, _ := forward.AsString(e.Doc.DataAt("request.httpInfo.requestURI"))
			logger.Infof("[%d]: kind=%d, id=%s, created=%s, uri=%s",
				i, e.Kind, e.Doc.Ref.ID, created.Format(iso8601Format), uri)
			if *optDump {
				fmt.Fprintf(os.Stderr, "%v\n", e.Doc.Data())
			}

			// Skip the doc that is too old.
			if time.Since(created) > *optExpire {
				if !*optWithoutCleaning {
					// Cleaning too old doc.
					_, err := e.Doc.Ref.Delete(ctx)
					if err != nil {
						logger.Errorf("*** doc.Delete: %v", err)
					}
				}
				continue
			}

			if e.Doc.Exists() && e.Kind == firestore.DocumentAdded {
				key := fmt.Sprintf("forward-consumer:doc:%s", e.Doc.Ref.ID)
				set, err := redisClient.SetNX(key, 1, time.Minute*5).Result()
				if err != nil {
					logger.Warnf("*** SetNX: key=%s, %v", key, err)
					// The doc is assumed to be processed.
				} else if !set {
					// Ignore the doc that has already processed.
					continue
				}

				ch <- e.Doc
			}
		}
	}
	close(ch)

	logger.Infof("Waiting workers exit")
	wg.Wait()
}
