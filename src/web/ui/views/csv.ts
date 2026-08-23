import type { Event } from "./types";

// CSV export of the (already filtered) audit rows — the "provable, exportable"
// story. toCsv is pure (string in, string out); downloadCsv is the one DOM
// action allowed in this presentational package (Blob/URL/<a> — no api/router).

// `multiHost` adds a `source` column (the poddle-cloud collector's per-event
// host attribution) — omitted by default so the single-instance OSS export
// stays byte-identical to before this column existed.
export function toCsv(rows: Event[], opts?: { multiHost?: boolean }): string {
  const cols: (keyof Event)[] = opts?.multiHost
    ? ["seq", "time", "source", "pod", "kind", "decision", "upstream", "method", "status", "detail"]
    : ["seq", "time", "pod", "kind", "decision", "upstream", "method", "status", "detail"];
  const esc = (v: unknown) => { const s = String(v ?? ""); return /[",\n]/.test(s) ? '"' + s.replace(/"/g, '""') + '"' : s; };
  const lines = rows.map((e) => cols.map((c) => esc(e[c])).join(","));
  return [cols.join(","), ...lines].join("\n");
}

export function downloadCsv(name: string, rows: Event[], opts?: { multiHost?: boolean }) {
  const blob = new Blob([toCsv(rows, opts)], { type: "text/csv;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.appendChild(a); a.click(); a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 0);
}
