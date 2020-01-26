package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go"
	"github.com/joho/godotenv"
	forward "github.com/tckz/personal-forward"
	"go.uber.org/zap"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var myName string
var logger *zap.SugaredLogger

type GRPCStatusHolder interface {
	GRPCStatus() *status.Status
}

var (
	optJSONKey      = flag.String("json-key", os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"), "/path/to/servicekey.json")
	optEndPointName = flag.String("endpoint-name", "", "Identity of endpoint")
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

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		s := <-sigCh
		logger.Infof("Received signal: %v", s)
		cancel()
	}()

	var opts []option.ClientOption
	if *optJSONKey != "" {
		opts = append(opts, option.WithCredentialsFile(*optJSONKey))
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

	ref, res, err := client.Collection("endpoints").Doc(*optEndPointName).Collection("requests").
		Add(ctx, map[string]interface{}{
			"created": firestore.ServerTimestamp,
			"request": map[string]interface{}{
				"httpInfo": map[string]interface{}{
					"method":     "GET",
					"requestURI": "/path/to/some?xxx=bbb",
				},
				"header": map[string]interface{}{
					"content-type": []string{"application/json"},
					"host":         []string{"somehost.example"},
				},
				"body": []byte(`{"some":"json or other content"}`),
			},
		})
	if err != nil {
		logger.Fatalf("*** Add: %v", err)
	}
	fmt.Printf("ref=%v, res.UpdateTime=%s\n", ref, res.UpdateTime)

	func() {
		ctx, cancel := context.WithTimeout(ctx, time.Second*5)
		defer cancel()

		it := ref.Snapshots(ctx)
		defer it.Stop()
		for {
			data, err := it.Next()
			if err != nil {
				if s, ok := err.(GRPCStatusHolder); err == iterator.Done || ok && s.GRPCStatus().Code() == codes.Canceled {
					break
				}
				logger.Fatalf("*** it.Next: %v, %T", err, err)
			}

			v, err := data.DataAt("response")
			if v == nil {
				continue
			}

			fmt.Fprintf(os.Stderr, "%v\n", data.Data())
			_, err = data.Ref.Delete(ctx)
			if err != nil {
				logger.With(zap.Error(err)).Errorf("*** data.Delete")
			}
			break
		}
	}()
}
