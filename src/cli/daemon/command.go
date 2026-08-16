// Package daemon implements `poddle daemon`: run the persistent poddled broker.
// It is normally auto-started by the CLI, not invoked by hand.
package daemon

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"git.dev.datadir.co/datadir/poddle/src/internal/poddled"
)

// NewCmd builds the (hidden) daemon command.
func NewCmd() *cobra.Command {
	var gatewayBind, egress, socket, l4RedisBind string
	c := &cobra.Command{
		Use:    "daemon",
		Short:  "Run the persistent poddled broker (usually auto-started)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if socket == "" {
				socket = poddled.SocketPath()
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return poddled.Serve(ctx, socket, gatewayBind, egress, l4RedisBind)
		},
	}
	c.Flags().StringVar(&gatewayBind, "gateway-bind", "0.0.0.0:0", "HTTP gateway bind address pods reach")
	c.Flags().StringVar(&egress, "egress", "redact", "egress redaction: redact|block|off")
	c.Flags().StringVar(&socket, "socket", "", "control socket path (default: XDG_RUNTIME_DIR/poddle/poddled.sock)")
	c.Flags().StringVar(&l4RedisBind, "l4-redis-bind", "0.0.0.0:0", "L4 Redis listener bind address pods reach")
	return c
}
