// Package usherv1 holds the generated gRPC stubs for the daemon API.
//
// The .pb.go files are checked in so that `nix build` needs nothing but a Go
// toolchain. Regenerate them from inside the devShell with `go generate ./...`
// after editing proto/usher/v1/usher.proto.
package usherv1

//go:generate protoc -I ../../../proto --go_out=../../.. --go_opt=module=github.com/hawkbawk/usher --go-grpc_out=../../.. --go-grpc_opt=module=github.com/hawkbawk/usher ../../../proto/usher/v1/usher.proto
