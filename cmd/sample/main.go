package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	firebase "firebase.google.com/go"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type GRPCStatusHolder interface {
	GRPCStatus() *status.Status
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sa := option.WithCredentialsFile(os.Args[1])
	app, err := firebase.NewApp(ctx, nil, sa)
	if err != nil {
		log.Fatalf("*** firebase.NewApp: %v", err)
	}

	client, err := app.Firestore(ctx)
	if err != nil {
		log.Fatalf("*** app.Firestore: %v", err)
	}
	defer client.Close()

	cols, err := client.Collections(context.Background()).GetAll()
	if err != nil {
		log.Fatalf("*** client.Collections: %v", err)
	}
	for i, e := range cols {
		fmt.Printf("[%d]%v\n", i, e.Path)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		s := <-sigCh
		log.Printf("received signal: %v", s)
		cancel()
	}()

	it := client.Collection("users").Doc("id").Collection("requests").Snapshots(ctx)
	defer it.Stop()
	for {
		data, err := it.Next()
		if err != nil {
			if s, ok := err.(GRPCStatusHolder); ok && s.GRPCStatus().Code() == codes.Canceled {
				break
			}
			log.Fatalf("*** it.Next: %v, %T", err, err)
		}
		for i, e := range data.Changes {
			fmt.Printf("[%d]: kind=%d, id=%s, old=%d, new=%d, data=%v\n",
				i, e.Kind, e.Doc.Ref.ID, e.OldIndex, e.NewIndex, e.Doc.Data())
		}
	}
	log.Print("done")
}
