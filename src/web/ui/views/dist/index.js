import { options as e } from "preact";
import { useEffect as t, useMemo as n, useState as r } from "preact/hooks";
//#region views/aggregate.ts
var i = (e) => {
	let t = (e || "").match(/redacted (\d+)/);
	return t ? +t[1] : 1;
};
function a(e) {
	let t = /* @__PURE__ */ new Set(), n = 0, r = 0, a = 0, o = 0, s = 0;
	for (let c of e) c.pod && t.add(c.pod), c.kind === "request" && n++, c.decision === "redact" && (r++, a += i(c.detail)), c.decision === "block" && o++, c.decision === "deny" && s++;
	return {
		pods: t.size,
		requests: n,
		redactions: r,
		secrets: a,
		blocked: o,
		denied: s
	};
}
function o(e, t) {
	let n = /* @__PURE__ */ new Map();
	for (let r of e) {
		if (!r.decision || !t.includes(r.decision)) continue;
		let e = `${r.pod || "—"}|${r.decision}|${r.upstream || "—"}`, a = n.get(e) || {
			pod: r.pod || "—",
			decision: r.decision,
			upstream: r.upstream || "—",
			count: 0,
			secrets: 0
		};
		a.count++, r.decision === "redact" && (a.secrets += i(r.detail)), n.set(e, a);
	}
	return [...n.values()].sort((e, t) => t.count - e.count);
}
var s = (e) => e && e.charAt(0).toUpperCase() + e.slice(1), c = (e) => s((e || "").replace(/\./g, " "));
function l(e) {
	let t = Math.max(0, Math.floor((Date.now() - new Date(e).getTime()) / 1e3));
	if (t < 5) return "just now";
	if (t < 60) return t + "s ago";
	let n = Math.floor(t / 60);
	if (n < 60) return n + "m ago";
	let r = Math.floor(n / 60);
	return r < 24 ? r + "h ago" : Math.floor(r / 24) + "d ago";
}
var u = (e) => e >= 85 ? "hot" : e >= 60 ? "warm" : "cool", d = 0;
Array.isArray;
function f(t, n, r, i, a, o) {
	n ||= {};
	var s, c, l = n;
	if ("ref" in l) for (c in l = {}, n) c == "ref" ? s = n[c] : l[c] = n[c];
	var u = {
		type: t,
		props: l,
		key: r,
		ref: s,
		__k: null,
		__: null,
		__b: 0,
		__e: null,
		__c: null,
		constructor: void 0,
		__v: --d,
		__i: -1,
		__u: 0,
		__source: a,
		__self: o
	};
	if (typeof t == "function" && (s = t.defaultProps)) for (c in s) l[c] === void 0 && (l[c] = s[c]);
	return e.vnode && e.vnode(u), u;
}
//#endregion
//#region views/Sparkline.tsx
function p({ data: e }) {
	let t = 2.5;
	if (e.length < 2) return /* @__PURE__ */ f("span", {
		class: "spark spark--empty faint",
		children: "╌"
	});
	let n = e.length - 1, r = (e) => Math.min(Math.max(e, 0), 100), i = (e) => t + e / n * (80 - t * 2), a = (e) => 17.5 - r(e) / 100 * (20 - t * 2), o = e.map((e, t) => `${i(t).toFixed(1)},${a(e).toFixed(1)}`).join(" "), s = e[n];
	return /* @__PURE__ */ f("svg", {
		class: "spark spark--" + u(s),
		width: 80,
		height: 20,
		viewBox: "0 0 80 20",
		preserveAspectRatio: "none",
		"aria-hidden": "true",
		children: [
			/* @__PURE__ */ f("polygon", {
				class: "spark__area",
				points: `${i(0).toFixed(1)},17.5 ${o} ${i(n).toFixed(1)},17.5`
			}),
			/* @__PURE__ */ f("polyline", {
				class: "spark__line",
				points: o,
				fill: "none"
			}),
			/* @__PURE__ */ f("circle", {
				class: "spark__dot",
				cx: i(n).toFixed(1),
				cy: a(s).toFixed(1),
				r: "1.9"
			})
		]
	});
}
//#endregion
//#region views/StatCard.tsx
function m({ n: e, label: t, tone: n }) {
	return /* @__PURE__ */ f("div", {
		class: "card" + (n ? " card--" + n : ""),
		children: [/* @__PURE__ */ f("div", {
			class: "card__num",
			children: e
		}), /* @__PURE__ */ f("div", {
			class: "card__label",
			children: t
		})]
	});
}
//#endregion
//#region views/SegmentedControl.tsx
function h({ value: e, options: t, onChange: n, ariaLabel: r }) {
	let i = Math.max(0, t.findIndex((t) => t.value === e));
	return /* @__PURE__ */ f("div", {
		class: "seg",
		role: "radiogroup",
		"aria-label": r,
		onKeyDown: (e) => {
			let r = i;
			if (e.key === "ArrowRight" || e.key === "ArrowDown") r = (i + 1) % t.length;
			else if (e.key === "ArrowLeft" || e.key === "ArrowUp") r = (i - 1 + t.length) % t.length;
			else return;
			e.preventDefault(), n(t[r].value);
		},
		children: t.map((t, r) => /* @__PURE__ */ f("button", {
			type: "button",
			role: "radio",
			"aria-checked": e === t.value,
			"data-tone": t.tone,
			tabIndex: r === i ? 0 : -1,
			class: "seg__opt" + (e === t.value ? " on" : ""),
			onClick: () => n(t.value),
			children: t.label
		}))
	});
}
//#endregion
//#region views/DecisionBadge.tsx
function g({ decision: e }) {
	return /* @__PURE__ */ f("span", {
		class: "decision d-" + (e || ""),
		children: e || /* @__PURE__ */ f("span", {
			class: "faint",
			children: "—"
		})
	});
}
//#endregion
//#region views/IntegrityBadge.tsx
function _({ v: e }) {
	return e ? e.ok ? /* @__PURE__ */ f("span", {
		class: "badge ok",
		children: "chain intact ✓"
	}) : /* @__PURE__ */ f("span", {
		class: "badge bad",
		children: [
			"chain broken @",
			e.brokenAt,
			" ✗"
		]
	}) : /* @__PURE__ */ f("span", {
		class: "badge",
		children: "verifying…"
	});
}
//#endregion
//#region views/AuditLogTable.tsx
var v = [
	{
		value: "",
		label: "All"
	},
	{
		value: "allow",
		label: "Allow",
		tone: "allow"
	},
	{
		value: "redact",
		label: "Redact",
		tone: "redact"
	},
	{
		value: "block",
		label: "Block",
		tone: "deny"
	},
	{
		value: "deny",
		label: "Deny",
		tone: "deny"
	}
];
function y({ events: e, initialPod: i }) {
	let [a, o] = r(i || ""), [u, d] = r("");
	t(() => {
		i && o(i);
	}, [i]);
	let p = n(() => e.filter((e) => {
		if (u && e.decision !== u) return !1;
		if (!a) return !0;
		let t = a.toLowerCase();
		return (e.pod || "").toLowerCase().includes(t) || (e.kind || "").toLowerCase().includes(t) || (e.upstream || "").toLowerCase().includes(t);
	}), [
		e,
		a,
		u
	]);
	return /* @__PURE__ */ f("div", { children: [/* @__PURE__ */ f("div", {
		class: "toolbar",
		children: [
			/* @__PURE__ */ f("input", {
				class: "grow",
				"aria-label": "Filter events by pod, kind, or upstream",
				placeholder: "Filter by pod, kind, or upstream…",
				value: a,
				onInput: (e) => o(e.target.value)
			}),
			/* @__PURE__ */ f(h, {
				value: u,
				options: v,
				onChange: d,
				ariaLabel: "filter by decision"
			}),
			/* @__PURE__ */ f("span", {
				class: "count",
				children: [p.length, " events"]
			})
		]
	}), /* @__PURE__ */ f("div", {
		class: "table-wrap",
		children: /* @__PURE__ */ f("table", {
			class: "dense",
			children: [/* @__PURE__ */ f("thead", { children: /* @__PURE__ */ f("tr", { children: [
				/* @__PURE__ */ f("th", {
					scope: "col",
					children: "time"
				}),
				/* @__PURE__ */ f("th", {
					scope: "col",
					children: "pod"
				}),
				/* @__PURE__ */ f("th", {
					scope: "col",
					children: "kind"
				}),
				/* @__PURE__ */ f("th", {
					scope: "col",
					children: "decision"
				}),
				/* @__PURE__ */ f("th", {
					scope: "col",
					children: "upstream"
				}),
				/* @__PURE__ */ f("th", {
					scope: "col",
					children: "detail"
				})
			] }) }), /* @__PURE__ */ f("tbody", { children: [p.length === 0 && /* @__PURE__ */ f("tr", { children: /* @__PURE__ */ f("td", {
				colSpan: 6,
				class: "empty",
				children: a || u ? "No events match your filter." : "Monitoring active — no events recorded yet."
			}) }), p.slice(0, 800).map((e) => /* @__PURE__ */ f("tr", { children: [
				/* @__PURE__ */ f("td", {
					class: "c-time",
					title: new Date(e.time).toLocaleString(),
					children: l(e.time)
				}),
				/* @__PURE__ */ f("td", {
					class: "c-pod",
					children: e.pod || /* @__PURE__ */ f("span", {
						class: "faint",
						children: "—"
					})
				}),
				/* @__PURE__ */ f("td", { children: c(e.kind) }),
				/* @__PURE__ */ f("td", { children: /* @__PURE__ */ f(g, { decision: e.decision }) }),
				/* @__PURE__ */ f("td", {
					class: "c-mono",
					children: e.upstream || /* @__PURE__ */ f("span", {
						class: "faint",
						children: "—"
					})
				}),
				/* @__PURE__ */ f("td", {
					class: "c-detail",
					children: s(e.detail || "")
				})
			] }, e.seq))] })]
		})
	})] });
}
//#endregion
export { y as AuditLogTable, g as DecisionBadge, _ as IntegrityBadge, h as SegmentedControl, p as Sparkline, m as StatCard, s as cap1, o as group, c as humanKind, l as relTime, a as summarise, u as threshTone };
