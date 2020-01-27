package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httputil"
	"net/http/pprof"
	"os"
	"os/signal"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"cloud.google.com/go/firestore"
	"cloud.google.com/go/profiler"
	"contrib.go.opencensus.io/exporter/stackdriver"
	firebase "firebase.google.com/go"
	"github.com/joho/godotenv"
	forward "github.com/tckz/personal-forward"
	"go.opencensus.io/exporter/stackdriver/propagation"
	"go.opencensus.io/plugin/ochttp"
	octrace "go.opencensus.io/trace"
	"go.uber.org/zap"
	goji "goji.io"
	"goji.io/pat"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
)

// When run under GAE, argv[0] is always "/usr/local/bin/start"
const myName = "forwarder"

var version string
var logger *zap.SugaredLogger

var (
	optJSONKey = flag.String("json-key", os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"), "/path/to/servicekey.json")
	optTimeout = flag.Duration("timeout", time.Second*60, "Timeout for waiting response")
	optDump    = flag.Bool("dump", false, "Dump request or not")
)

func init() {
	godotenv.Load()

	zl, err := forward.NewLogger()
	if err != nil {
		panic(err)
	}
	logger = zl.Sugar().With(zap.String("app", myName))

	{
		logger := logger.With("type", "init")

		s := &strings.Builder{}
		src := os.Environ()
		dst := make([]string, len(src))
		copy(dst, src)
		sort.Strings(dst)
		for _, e := range dst {
			fmt.Fprintf(s, "%s\n", e)
		}
		logger.Infof("%s", s.String())
	}
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

func initFirestore(ctx context.Context) *firestore.Client {
	var opts []option.ClientOption
	if *optJSONKey != "" {
		opts = append(opts, option.WithCredentialsFile(*optJSONKey))
	}

	app, err := firebase.NewApp(ctx, nil, opts...)
	if err != nil {
		logger.Panicf("*** firebase.NewApp: %v", err)
	}

	client, err := app.Firestore(ctx)
	if err != nil {
		logger.Panicf("*** app.Firestore: %v", err)
	}

	return client
}

func run() {
	defaultBind := ":3000"
	if port := os.Getenv("PORT"); port != "" {
		defaultBind = fmt.Sprintf(":%s", port)
	}

	defaultSdProfiler, _ := strconv.ParseBool(os.Getenv("ENABLE_SD_PROFILER"))

	projectID := flag.String("project-id", os.Getenv("GOOGLE_CLOUD_PROJECT"), "ProjectID of GCP")
	bind := flag.String("bind", defaultBind, "Listen addr:port")
	bindStats := flag.String("bind-stats", ":3001", "Listen addr:port for pprof")
	enableSDProfiler := flag.Bool("enable-sd-profiler", defaultSdProfiler, "Enable Stackdriver Profiler")
	timeoutSec := flag.Int("shutdown-timeout-sec", 5, "Timeout sec for waiting shutdown")
	blockProfileRate := flag.Int("block-profile-rate", 0, "Number of runtime.MemProfileRate. Value 1 is the finest")
	optEndPointName := flag.String("endpoint-name", os.Getenv("ENDPOINT_NAME"), "Identity of endpoint")
	flag.Parse()

	// Log at any loglevel
	logger.Infof("Listen: %s, ver=%s, args=%v", *bind, version, os.Args)

	if *blockProfileRate > 0 {
		runtime.SetBlockProfileRate(*blockProfileRate)
	}

	if *projectID == "" {
		logger.Panicf("ProjectID must be specified")
	}

	endPointName := *optEndPointName
	if endPointName == "" {
		// default EP name under GAE
		if svcName := os.Getenv("GAE_SERVICE"); svcName != "" {
			endPointName = svcName
		} else {
			logger.Panicf("endpoint-name must be specified")
		}
	}

	if *bindStats != "" {
		statsMux := http.NewServeMux()
		statsMux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		statsMux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		statsMux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		statsMux.HandleFunc("/debug/pprof/trace", pprof.Trace)
		statsMux.HandleFunc("/debug/pprof/", pprof.Index)
		go func() {
			if err := http.ListenAndServe(*bindStats, statsMux); err != nil && err != http.ErrServerClosed {
				logger.With(zap.Error(err)).Fatalf("*** http.ListenAndServe")
			}
		}()
	}

	var client *firestore.Client
	func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
		defer cancel()
		client = initFirestore(ctx)
	}()
	defer client.Close()

	if *enableSDProfiler {
		logger.Infof("Enable Stackdriver profiler")
		if err := profiler.Start(profiler.Config{
			ProjectID:      *projectID,
			Service:        myName,
			DebugLogging:   false,
			ServiceVersion: version,
		}); err != nil {
			logger.Fatalf("*** Failed to profiler.Start(): %v", err)
		}
	}

	exporter, err := stackdriver.NewExporter(stackdriver.Options{})
	if err != nil {
		logger.Fatalf("*** stackdriver.NewExporter(): %v", err)
	}
	defer exporter.Flush()
	octrace.RegisterExporter(exporter)
	octrace.ApplyConfig(octrace.Config{DefaultSampler: octrace.AlwaysSample()})

	propg := &propagation.HTTPFormat{}

	mux := goji.NewMux()

	// MW for tracing
	mux.Use(func(h http.Handler) http.Handler {
		return &ochttp.Handler{
			Handler:     h,
			Propagation: &propagation.HTTPFormat{},
			FormatSpanName: func(r *http.Request) string {
				return myName + ":" + r.URL.Path
			},
		}
	})

	// MW for logging/recover
	mux.Use(func(handler http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			logger := logger
			span := octrace.FromContext(r.Context())
			if span != nil {
				sc := span.SpanContext()
				tid := sc.TraceID.String()
				logger = logger.With(zap.String("logging.googleapis.com/trace",
					fmt.Sprintf("projects/%s/traces/%s", *projectID, tid)))
			}

			r = r.WithContext(forward.WithLogger(r.Context(), logger.Desugar()))

			defer func() {
				if r := recover(); r != nil {
					var err error
					if e, ok := r.(error); ok {
						err = e
					} else {
						err = fmt.Errorf("%v", r)
					}
					logger.With(zap.Stack("stack"), zap.Error(err)).
						Errorf("*** panic: %v", r)
					w.WriteHeader(http.StatusInternalServerError)
					if span != nil {
						span.SetStatus(octrace.Status{
							Code:    int32(codes.Internal),
							Message: err.Error(),
						})
					}
				}
			}()

			handler.ServeHTTP(w, r)
		})
	})

	mux.HandleFunc(pat.New("/*"), func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		span := octrace.FromContext(ctx)
		logger := forward.ExtractLogger(ctx).Sugar()

		if *optDump {
			b, err := httputil.DumpRequest(r, true)
			if err != nil {
				logger.Errorf("*** httputil.DumpRequest: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			fmt.Fprintln(os.Stderr, string(b))
		}

		b, err := ioutil.ReadAll(r.Body)
		if err != nil {
			logger.Errorf("*** ReadAll: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		header := r.Header
		if span != nil {
			propg.SpanContextToRequest(span.SpanContext(), r)
		}

		ref, _, err := client.Collection("endpoints").Doc(endPointName).Collection("requests").
			Add(ctx, map[string]interface{}{
				"created": firestore.ServerTimestamp,
				"request": map[string]interface{}{
					"httpInfo": map[string]interface{}{
						"method":     r.Method,
						"requestURI": r.RequestURI,
					},
					"header": header,
					"body":   b,
				},
			})
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			logger.Errorf("*** Add: %v", err)
			return
		}
		logger.Infof("created=%s", ref.Path)

		// wait response
		func() {
			ctx, cancel := context.WithTimeout(ctx, *optTimeout)
			defer cancel()

			it := ref.Snapshots(ctx)
			defer it.Stop()
			for {
				data, err := it.Next()
				if err != nil {
					if s, ok := err.(forward.GRPCStatusHolder); err == iterator.Done || ok && s.GRPCStatus().Code() == codes.Canceled {
						break
					}
					w.WriteHeader(http.StatusGatewayTimeout)
					logger.Errorf("*** it.Next: %v", err)
					return
				}

				v, err := data.DataAt("response")
				if v == nil {
					continue
				}

				if errText, _ := forward.AsString(data.DataAt("response.error")); errText != "" {
					w.WriteHeader(http.StatusInternalServerError)
					logger.Infof("errText: %s", errText)
					return
				}

				code, _ := forward.AsInt64(data.DataAt("response.statusCode"))
				body, _ := forward.AsByte(data.DataAt("response.body"))
				header, _ := forward.AsHeader(data.DataAt("response.header"))
				logger.Infof("response: code=%d, header=%v", code, header)

				// construct response
				for k, values := range header {
					for _, e := range values {
						w.Header().Add(k, e)
					}
				}
				w.WriteHeader(int(code))
				io.Copy(w, bytes.NewReader(body))

				_, err = data.Ref.Delete(ctx)
				if err != nil {
					logger.With(zap.Error(err)).Errorf("*** data.Delete")
					// Failed to delete but response is done and received.
					// So it does not override status code.
				}
				break
			}
		}()

	})

	server := &http.Server{
		Addr:    *bind,
		Handler: mux,
	}

	go func() {
		logger.Infof("Start to Serve: %s", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatalf("*** Failed to ListenAndServe(): %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	s := <-sigCh
	logger.Infof("Receive signal: %v", s)

	func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeoutSec)*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			logger.Fatalf("*** Failed to Shutdown(): %v", err)
		}
	}()
}
