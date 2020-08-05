package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	forward "github.com/tckz/personal-forward"
	"go.uber.org/zap"
)

var myName string
var version string
var logger *zap.SugaredLogger

var (
	optBind    = flag.String("bind", ":3010", "addr:port")
	optEcho    = flag.Bool("echo", false, "Echo response body or not")
	optVersion = flag.Bool("version", false, "Show version")
)

func init() {
	godotenv.Load()

	myName = filepath.Base(os.Args[0])

	zl, err := forward.NewLogger()
	if err != nil {
		panic(err)
	}
	logger = zl.Sugar().With(zap.String("app", myName))
}

func main() {
	flag.Parse()
	if *optVersion {
		fmt.Printf("%s\n", version)
		return
	}

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
	logger.Infof("args=%#v, ver=%s", os.Args, version)

	run()

	logger.Info("exit")

}

func run() {

	m := http.NewServeMux()
	m.HandleFunc("/", func(ow http.ResponseWriter, r *http.Request) {
		w := forward.NewResponseWriterWrapper(ow)
		begin := time.Now()

		defer func() {
			logger := logger
			if r := recover(); r != nil {
				w.WriteHeader(http.StatusInternalServerError)
				err, ok := r.(error)
				if !ok {
					err = fmt.Errorf("panic: %v", r)
				}
				logger = logger.With(zap.Error(err), zap.Stack("stack"))
			}

			dur := time.Since(begin)
			logger.With(
				zap.Int("status", w.StatusCode),
				zap.String("method", r.Method),
				zap.String("uri", r.RequestURI),
				zap.String("remote", r.RemoteAddr),
				zap.Float64("msec", float64(dur)/float64(time.Millisecond)),
			).
				Infof("done: %s, %s", dur, r.RequestURI)
		}()

		b, err := httputil.DumpRequest(r, true)
		if err != nil {
			logger.Errorf("*** httputil.DumpRequest: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fmt.Fprintln(os.Stderr, string(b))

		if *optEcho {
			if h := r.Header.Get("content-type"); h != "" {
				w.Header().Set("content-type", h)
			}
			io.Copy(w, r.Body)
		}
	})

	server := &http.Server{
		Addr:    *optBind,
		Handler: m,
	}

	logger.Infof("Start to Serve: %s", server.Addr)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatalf("*** ListenAndServe(): %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	s := <-sigCh
	logger.Infof("Received signal: %v", s)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server.Shutdown(ctx)
}
