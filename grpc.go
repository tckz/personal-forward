package forward

import "google.golang.org/grpc/status"

type GRPCStatusHolder interface {
	GRPCStatus() *status.Status
}
