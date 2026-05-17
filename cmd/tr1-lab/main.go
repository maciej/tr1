package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"tr1/internal/tr1"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := tr1.NewLabCommand(ctx).Execute(); err != nil && !errors.Is(err, context.Canceled) {
		tr1.Fatal("tr1-lab", err)
	}
}
