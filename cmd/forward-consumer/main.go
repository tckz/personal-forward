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
	"sync"
	"syscall"
	"time"

	"cloud.google.com/go/firestore"
	"contrib.go.opencensus.io/exporter/stackdriver"
	firebase "firebase.google.com/go"
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

var (
	optJSONKey      = flag.String("json-key", os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"), "/path/to/servicekey.json")
	optWorkers      = flag.Int("workers", 8, "Number of groutines to process request")
	optDump         = flag.Bool("dump", false, "Dump received request or not")
	optExpire       = flag.Duration("expire", time.Minute*2, "Ignore too old request")
	optEndPointName = flag.String("endpoint-name", "", "Identity of endpoint")
	optCleaning     = flag.Bool("with-cleaning", false, "Delete request documents that is expired")
	optTarget       = flag.String("target", "http://localhost:3010", "")
)

func init() {
	godotenv.Load()

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
	run()

	logger.Info("exit")

}

func run() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if *optEndPointName == "" {
		logger.Fatalf("*** --endpoint-name must be specified")
	}

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

	consumer := &Consumer{
		Propagation: &propagation.HTTPFormat{},
		Client: &http.Client{
			Transport: &ochttp.Transport{
				Propagation:    &propagation.HTTPFormat{},
				NewClientTrace: ochttp.NewSpanAnnotatingClientTrace,
			},
		},
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
		logger.Infof("Waiting workers exit")
		wg.Wait()
	}()

	targetURL, err := url.Parse(*optTarget)
	if err != nil {
		logger.Fatalf("*** url.Parse: %v", err)
	}
	for i := 0; i < *optWorkers; i++ {
		wg.Add(1)
		logger := logger.With(zap.Int("worker", i))
		go func() {
			defer wg.Done()

			ctx := forward.WithLogger(ctx, logger.Desugar())

			for doc := range ch {
				func() {
					ctx, cancel := context.WithTimeout(ctx, time.Second*30)
					defer cancel()

					err := consumer.ForwardRequest(ctx, targetURL, doc)
					if err != nil {
						logger.With(zap.Error(err)).Errorf("*** forwardRequest")
					}
				}()
			}
		}()
	}

	logger.Infof("Listening endpoint=%s", *optEndPointName)
	it := client.Collection("endpoints").Doc(*optEndPointName).Collection("requests").Snapshots(ctx)
	defer it.Stop()
	for {
		data, err := it.Next()
		if err != nil {
			if s, ok := err.(forward.GRPCStatusHolder); err == iterator.Done || ok && s.GRPCStatus().Code() == codes.Canceled {
				break
			}
			logger.Fatalf("*** it.Next: %v", err)
		}

		for i, e := range data.Changes {
			logger.Infof("[%d]: kind=%d, path=%s, old=%d, new=%d",
				i, e.Kind, e.Doc.Ref.Path, e.OldIndex, e.NewIndex)
			if *optDump {
				fmt.Fprintf(os.Stderr, "%v\n", e.Doc.Data())
			}

			created, _ := forward.AsTime(e.Doc.DataAt("created"))
			if time.Since(created) > *optExpire {
				if *optCleaning {
					_, err := e.Doc.Ref.Delete(ctx)
					if err != nil {
						logger.Errorf("*** doc.Delete: %v", err)
					}
				}
				continue
			}

			if e.Doc.Exists() && e.Kind == firestore.DocumentAdded {
				ch <- e.Doc
			}
		}
	}
	close(ch)
}
