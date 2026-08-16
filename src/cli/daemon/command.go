// Package daemon implements `poddle daemon`: run the persistent poddled broker.
// It is normally auto-started by the CLI, not invoked by hand.
package daemon

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/datadir-lab/poddle/src/internal/poddled"
)

// NewCmd builds the (hidden) daemon command.
func NewCmd() *cobra.Command {
	var gatewayBind, egress, socket, l4RedisBind, l4PostgresBind string
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
			return poddled.Serve(ctx, socket, gatewayBind, egress, l4RedisBind, l4PostgresBind)
		},
	}
	c.Flags().StringVar(&gatewayBind, "gateway-bind", "0.0.0.0:0", "HTTP gateway bind address pods reach")
	c.Flags().StringVar(&egress, "egress", "redact", "egress redaction: redact|block|off")
	c.Flags().StringVar(&socket, "socket", "", "control socket path (default: XDG_RUNTIME_DIR/poddle/poddled.sock)")
	c.Flags().StringVar(&l4RedisBind, "l4-redis-bind", "0.0.0.0:0", "L4 Redis listener bind address pods reach")
	c.Flags().StringVar(&l4PostgresBind, "l4-postgres-bind", "0.0.0.0:0", "L4 Postgres listener bind address pods reach")
	c.AddCommand(statusCmd())
	return c
}

// statusCmd builds `poddle daemon status`: report whether poddled is running and
// what it's serving.
func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether poddled is running and what it is serving",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			s, err := poddled.NewClient("").Status()
			if err != nil {
				fmt.Fprintln(out, "poddled: not running")
				return nil
			}
			fmt.Fprintln(out, "poddled: running")
			fmt.Fprintf(out, "  gateway:  %s\n", s.Gateway)
			if s.Redis != "" {
				fmt.Fprintf(out, "  redis:    %s\n", s.Redis)
			}
			if s.Postgres != "" {
				fmt.Fprintf(out, "  postgres: %s\n", s.Postgres)
			}
			if len(s.Pods) == 0 {
				fmt.Fprintln(out, "  pods:     none")
				return nil
			}
			fmt.Fprintf(out, "  pods:     %d\n", len(s.Pods))
			for name, n := range s.Pods {
				fmt.Fprintf(out, "    - %s (%d handles)\n", name, n)
			}
			return nil
		},
	}
}
