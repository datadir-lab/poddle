// Package daemon implements `poddle daemon`: run the persistent poddled broker.
// It is normally auto-started by the CLI, not invoked by hand.
package daemon

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/datadir-lab/poddle/src/internal/audit"
	"github.com/datadir-lab/poddle/src/internal/poddled"
)

// NewCmd builds the (hidden) daemon command.
func NewCmd() *cobra.Command {
	var gatewayBind, egress, socket, l4RedisBind, l4PostgresBind, forwardBind string
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
			return poddled.Serve(ctx, socket, gatewayBind, egress, l4RedisBind, l4PostgresBind, forwardBind)
		},
	}
	c.Flags().StringVar(&gatewayBind, "gateway-bind", "0.0.0.0:0", "HTTP gateway bind address pods reach")
	c.Flags().StringVar(&egress, "egress", "redact", "egress redaction: redact|block|off")
	c.Flags().StringVar(&socket, "socket", "", "control socket path (default: XDG_RUNTIME_DIR/poddle/poddled.sock)")
	c.Flags().StringVar(&l4RedisBind, "l4-redis-bind", "0.0.0.0:0", "L4 Redis listener bind address pods reach")
	c.Flags().StringVar(&l4PostgresBind, "l4-postgres-bind", "0.0.0.0:0", "L4 Postgres listener bind address pods reach")
	c.Flags().StringVar(&forwardBind, "forward-bind", "0.0.0.0:0", "egress forward-proxy bind address pods reach (HTTP_PROXY)")
	c.AddCommand(statusCmd())
	c.AddCommand(auditCmd())
	c.AddCommand(autoscaledCmd())
	return c
}

// autoscaledCmd builds `poddle daemon autoscaled`: run the reactive autoscaler
// on the host (where podman and the poddle binary live). It is host-side because
// it shells `podman ps` / `poddle move`, which the distroless broker container
// cannot. Normally auto-started by `up --autoscale`, not invoked by hand.
func autoscaledCmd() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:    "autoscaled",
		Short:  "Run the host-side reactive autoscaler (usually auto-started)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if socket == "" {
				socket = poddled.SocketPath()
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return poddled.RunHostAutoscaler(ctx, socket)
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "control socket path (default: XDG_RUNTIME_DIR/poddle/poddled.sock)")
	return c
}

// auditCmd builds `poddle daemon audit`: show recent audit events from the
// daemon's tamper-evident log (proxied requests, redactions/blocks, handle +
// pod lifecycle, autoscale actions).
func auditCmd() *cobra.Command {
	var pod, kind, decision string
	var limit int
	var verify bool
	c := &cobra.Command{
		Use:   "audit",
		Short: "Show recent audit events (requests, redactions, blocks, handle + pod lifecycle)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			client := poddled.NewClient("")
			if verify {
				ok, at, err := client.VerifyAudit()
				if err != nil {
					fmt.Fprintln(out, "poddled: not running")
					return nil
				}
				if ok {
					fmt.Fprintln(out, "audit chain: intact ✓")
				} else {
					fmt.Fprintf(out, "audit chain: BROKEN at seq %d ✗\n", at)
				}
				return nil
			}
			events, err := client.Audits(audit.Filter{
				Pod: pod, Kind: kind, Decision: decision, Limit: limit,
			})
			if err != nil {
				fmt.Fprintln(out, "poddled: not running")
				return nil
			}
			if len(events) == 0 {
				fmt.Fprintln(out, "no audit events")
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "TIME\tPOD\tKIND\tDECISION\tUPSTREAM\tDETAIL")
			for _, e := range events {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					e.Time.Format("15:04:05"), dash(e.Pod), e.Kind, dash(string(e.Decision)), dash(e.Upstream), e.Detail)
			}
			return w.Flush()
		},
	}
	c.Flags().StringVar(&pod, "pod", "", "filter by pod")
	c.Flags().StringVar(&kind, "kind", "", "filter by kind (request|redact|block|handle.issue|pod.up|...)")
	c.Flags().StringVar(&decision, "decision", "", "filter by decision (allow|redact|block|deny)")
	c.Flags().IntVar(&limit, "limit", 50, "maximum events to show")
	c.Flags().BoolVar(&verify, "verify", false, "verify the audit log's hash chain instead of listing events")
	return c
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
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
			renderStatus(out, s)
			return nil
		},
	}
}

// renderStatus prints a poddled Status the way `poddle daemon status` reports
// it. Split out from statusCmd's RunE so it's testable without a live daemon.
func renderStatus(out io.Writer, s poddled.Status) {
	fmt.Fprintln(out, "poddled: running")
	fmt.Fprintf(out, "  gateway:  %s\n", s.Gateway)
	if s.BrokerPrivsep {
		fmt.Fprintln(out, "  broker:   two-process (privilege-separated keeper)")
	}
	if s.Redis != "" {
		fmt.Fprintf(out, "  redis:    %s\n", s.Redis)
	}
	if s.Postgres != "" {
		fmt.Fprintf(out, "  postgres: %s\n", s.Postgres)
	}
	if len(s.Pods) == 0 {
		fmt.Fprintln(out, "  pods:     none")
	} else {
		fmt.Fprintf(out, "  pods:     %d\n", len(s.Pods))
		for name, n := range s.Pods {
			fmt.Fprintf(out, "    - %s (%d handles)\n", name, n)
		}
	}
	if len(s.Events) > 0 {
		fmt.Fprintln(out, "  autoscale:")
		for _, e := range s.Events {
			fmt.Fprintf(out, "    - %s\n", e)
		}
	}
	if len(s.NeedsReauth) > 0 {
		fmt.Fprintln(out, "  needs reauth:")
		for _, name := range s.NeedsReauth {
			fmt.Fprintf(out, "    - %s (run: poddle connect reauth %s)\n", name, name)
		}
	}
}
