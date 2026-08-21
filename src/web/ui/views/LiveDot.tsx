import type { Conn } from "./types";

// LiveDot reflects the audit stream's connection: live, reconnecting, or connecting.
export function LiveDot({ status }: { status: Conn }) {
  const txt = status === "live" ? "Live" : status === "down" ? "Reconnecting" : "Connecting";
  return (
    <span class={"live live--" + status} title={"Audit stream: " + txt} role="status">
      <span class="live__dot" aria-hidden="true" />{txt}
    </span>
  );
}
