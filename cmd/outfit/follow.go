package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// followUntilInterrupted runs a polling loop under a context that an interrupt
// cancels, and treats that cancellation as success — the user asking a follow
// to stop is not a failure. Both `outfit remote logs -f` and `outfit fleet
// logs -f` follow output this way, and the wiring is the part they genuinely
// share: what they poll, and what they do with the answers, is different
// enough that a common loop would fit neither.
//
// The loop is handed the context and is expected to return nil once it is
// cancelled.
func followUntilInterrupted(loop func(ctx context.Context) error) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		cancel()
	}()

	return loop(ctx)
}
