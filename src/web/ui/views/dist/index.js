import { Fragment as e, options as t } from "preact";
import { useEffect as n, useMemo as r, useState as i } from "preact/hooks";
//#region views/types.ts
var a = [
	"GET",
	"POST",
	"PUT",
	"PATCH",
	"DELETE",
	"HEAD",
	"OPTIONS"
], o = [
	{
		value: "",
		label: "All"
	},
	{
		value: "15m",
		label: "15m"
	},
	{
		value: "1h",
		label: "1h"
	},
	{
		value: "24h",
		label: "24h"
	}
], s = {
	"15m": 9e5,
	"1h": 36e5,
	"24h": 864e5
};
//#endregion
//#region views/policy-eval.ts
function c(e, t) {
	for (let n of t) if (n === e || n.startsWith(".") && (e.endsWith(n) || e === n.slice(1))) return !0;
	return !1;
}
function l(e, t) {
	if (!e) return null;
	if (t in e) return e[t];
	for (let n in e) if (n.startsWith(".") && (t.endsWith(n) || t === n.slice(1))) return e[n];
	return null;
}
function u(e, t, n) {
	if (c(t, e.deny_upstreams || [])) return {
		allow: !1,
		reason: "on the deny-list"
	};
	if ((e.allow_upstreams || []).length > 0 && !c(t, e.allow_upstreams || [])) return {
		allow: !1,
		reason: "not allow-listed"
	};
	let r = l(e.methods, t);
	return r && n && n !== "CONNECT" && !r.some((e) => e.toUpperCase() === n.toUpperCase()) ? {
		allow: !1,
		reason: n + " not allowed here"
	} : {
		allow: !0,
		reason: ""
	};
}
function d(e, t) {
	let n = t.filter((e) => e.kind === "request" && e.upstream), r = /* @__PURE__ */ new Map(), i = 0;
	for (let t of n) {
		let n = u(e, t.upstream, t.method || "");
		if (n.allow) continue;
		i++;
		let a = `${t.method || ""}|${t.upstream}`, o = r.get(a) || {
			upstream: t.upstream,
			method: t.method || "",
			reason: n.reason,
			count: 0
		};
		o.count++, r.set(a, o);
	}
	return {
		total: n.length,
		denied: i,
		rows: [...r.values()].sort((e, t) => t.count - e.count)
	};
}
function f(e) {
	let t = e.methods || {};
	return [.../* @__PURE__ */ new Set([...e.allow_upstreams || [], ...Object.keys(t)])].map((e) => ({
		host: e,
		methods: t[e] || [],
		open: !1
	}));
}
//#endregion
//#region views/aggregate.ts
var p = (e) => {
	let t = (e || "").match(/redacted (\d+)/);
	return t ? +t[1] : 1;
};
function m(e) {
	let t = /* @__PURE__ */ new Set(), n = 0, r = 0, i = 0, a = 0, o = 0;
	for (let s of e) s.pod && t.add(s.pod), s.kind === "request" && n++, s.decision === "redact" && (r++, i += p(s.detail)), s.decision === "block" && a++, s.decision === "deny" && o++;
	return {
		pods: t.size,
		requests: n,
		redactions: r,
		secrets: i,
		blocked: a,
		denied: o
	};
}
function h(e, t) {
	let n = /* @__PURE__ */ new Map();
	for (let r of e) {
		if (!r.decision || !t.includes(r.decision)) continue;
		let e = `${r.pod || "—"}|${r.decision}|${r.upstream || "—"}`, i = n.get(e) || {
			pod: r.pod || "—",
			decision: r.decision,
			upstream: r.upstream || "—",
			count: 0,
			secrets: 0
		};
		i.count++, r.decision === "redact" && (i.secrets += p(r.detail)), n.set(e, i);
	}
	return [...n.values()].sort((e, t) => t.count - e.count);
}
var g = (e) => e && e.charAt(0).toUpperCase() + e.slice(1), _ = (e) => g((e || "").replace(/\./g, " "));
function v(e) {
	let t = Math.max(0, Math.floor((Date.now() - new Date(e).getTime()) / 1e3));
	if (t < 5) return "just now";
	if (t < 60) return t + "s ago";
	let n = Math.floor(t / 60);
	if (n < 60) return n + "m ago";
	let r = Math.floor(n / 60);
	return r < 24 ? r + "h ago" : Math.floor(r / 24) + "d ago";
}
var y = (e) => e >= 85 ? "hot" : e >= 60 ? "warm" : "cool", b = [
	{
		key: "allow",
		label: "Allow",
		icon: "check"
	},
	{
		key: "redact",
		label: "Redact",
		icon: "eyeoff"
	},
	{
		key: "deny",
		label: "Deny",
		icon: "ban"
	},
	{
		key: "block",
		label: "Block",
		icon: "octagon"
	}
];
function x(e) {
	let t = {
		allow: 0,
		redact: 0,
		deny: 0,
		block: 0
	};
	for (let n of e) n.decision && n.decision in t && t[n.decision]++;
	return t;
}
function S(e) {
	let t = /* @__PURE__ */ new Map();
	for (let n of e) {
		if (n.kind !== "request" || !n.upstream) continue;
		let e = t.get(n.upstream) || {
			upstream: n.upstream,
			total: 0,
			allow: 0,
			redact: 0,
			deny: 0,
			block: 0,
			secrets: 0,
			pods: /* @__PURE__ */ new Set()
		};
		switch (e.total++, n.pod && e.pods.add(n.pod), n.decision) {
			case "allow":
				e.allow++;
				break;
			case "redact":
				e.redact++, e.secrets += p(n.detail);
				break;
			case "deny":
				e.deny++;
				break;
			case "block": e.block++;
		}
		t.set(n.upstream, e);
	}
	return [...t.values()].sort((e, t) => t.total - e.total);
}
function C(e) {
	return (t) => {
		(t.key === "Enter" || t.key === " ") && (t.preventDefault(), e());
	};
}
//#endregion
//#region views/chart.ts
function w(e, t = 24) {
	let n = e.filter((e) => e.kind === "request" && e.time && !Number.isNaN(new Date(e.time).getTime()));
	if (n.length < 2) return [];
	let r = Infinity, i = -Infinity, a = n.map((e) => {
		let t = new Date(e.time).getTime();
		return t < r && (r = t), t > i && (i = t), t;
	});
	i <= r && (i = r + 1);
	let o = (i - r) / t, s = Array.from({ length: t }, (e, t) => ({
		t0: r + t * o,
		req: 0,
		intervened: 0
	}));
	return n.forEach((e, n) => {
		let i = Math.floor((a[n] - r) / o);
		i < 0 ? i = 0 : i >= t && (i = t - 1), s[i].req++, (e.decision === "redact" || e.decision === "deny" || e.decision === "block") && s[i].intervened++;
	}), s;
}
//#endregion
//#region node_modules/preact/jsx-runtime/dist/jsxRuntime.module.js
var T = 0;
Array.isArray;
function E(e, n, r, i, a, o) {
	n ||= {};
	var s, c, l = n;
	if ("ref" in l) for (c in l = {}, n) c == "ref" ? s = n[c] : l[c] = n[c];
	var u = {
		type: e,
		props: l,
		key: r,
		ref: s,
		__k: null,
		__: null,
		__b: 0,
		__e: null,
		__c: null,
		constructor: void 0,
		__v: --T,
		__i: -1,
		__u: 0,
		__source: a,
		__self: o
	};
	if (typeof e == "function" && (s = e.defaultProps)) for (c in s) l[c] === void 0 && (l[c] = s[c]);
	return t.vnode && t.vnode(u), u;
}
//#endregion
//#region views/Sparkline.tsx
function D({ data: e }) {
	let t = 2.5;
	if (e.length < 2) return /* @__PURE__ */ E("span", {
		class: "spark spark--empty faint",
		children: "╌"
	});
	let n = e.length - 1, r = (e) => Math.min(Math.max(e, 0), 100), i = (e) => t + e / n * (80 - t * 2), a = (e) => 17.5 - r(e) / 100 * (20 - t * 2), o = e.map((e, t) => `${i(t).toFixed(1)},${a(e).toFixed(1)}`).join(" "), s = e[n];
	return /* @__PURE__ */ E("svg", {
		class: "spark spark--" + y(s),
		width: 80,
		height: 20,
		viewBox: "0 0 80 20",
		preserveAspectRatio: "none",
		"aria-hidden": "true",
		children: [
			/* @__PURE__ */ E("polygon", {
				class: "spark__area",
				points: `${i(0).toFixed(1)},17.5 ${o} ${i(n).toFixed(1)},17.5`
			}),
			/* @__PURE__ */ E("polyline", {
				class: "spark__line",
				points: o,
				fill: "none"
			}),
			/* @__PURE__ */ E("circle", {
				class: "spark__dot",
				cx: i(n).toFixed(1),
				cy: a(s).toFixed(1),
				r: "1.9"
			})
		]
	});
}
//#endregion
//#region views/Icon.tsx
var O = {
	overview: () => /* @__PURE__ */ E(e, { children: [
		/* @__PURE__ */ E("rect", {
			x: "3",
			y: "3",
			width: "7",
			height: "7",
			rx: "1.4"
		}),
		/* @__PURE__ */ E("rect", {
			x: "14",
			y: "3",
			width: "7",
			height: "7",
			rx: "1.4"
		}),
		/* @__PURE__ */ E("rect", {
			x: "14",
			y: "14",
			width: "7",
			height: "7",
			rx: "1.4"
		}),
		/* @__PURE__ */ E("rect", {
			x: "3",
			y: "14",
			width: "7",
			height: "7",
			rx: "1.4"
		})
	] }),
	pods: () => /* @__PURE__ */ E(e, { children: [
		/* @__PURE__ */ E("path", { d: "M21 8v8a2 2 0 0 1-1 1.73l-7 4a2 2 0 0 1-2 0l-7-4A2 2 0 0 1 3 16V8a2 2 0 0 1 1-1.73l7-4a2 2 0 0 1 2 0l7 4A2 2 0 0 1 21 8Z" }),
		/* @__PURE__ */ E("path", { d: "m3.3 7 8.7 5 8.7-5" }),
		/* @__PURE__ */ E("path", { d: "M12 22V12" })
	] }),
	audit: () => /* @__PURE__ */ E("path", { d: "M22 12h-4l-3 9L9 3l-3 9H2" }),
	policies: () => /* @__PURE__ */ E(e, { children: [/* @__PURE__ */ E("path", { d: "M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67 0C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1Z" }), /* @__PURE__ */ E("path", { d: "m9 12 2 2 4-4" })] }),
	globe: () => /* @__PURE__ */ E(e, { children: [/* @__PURE__ */ E("circle", {
		cx: "12",
		cy: "12",
		r: "10"
	}), /* @__PURE__ */ E("path", { d: "M12 2a15 15 0 0 1 0 20 15 15 0 0 1 0-20M2 12h20" })] }),
	eyeoff: () => /* @__PURE__ */ E(e, { children: [
		/* @__PURE__ */ E("path", { d: "M9.88 9.88a3 3 0 1 0 4.24 4.24" }),
		/* @__PURE__ */ E("path", { d: "M10.73 5.08A11 11 0 0 1 12 5c7 0 10 7 10 7a13 13 0 0 1-1.67 2.68" }),
		/* @__PURE__ */ E("path", { d: "M6.61 6.61A13 13 0 0 0 2 12s3 7 10 7a11 11 0 0 0 5.39-1.39" }),
		/* @__PURE__ */ E("line", {
			x1: "2",
			y1: "2",
			x2: "22",
			y2: "22"
		})
	] }),
	ban: () => /* @__PURE__ */ E(e, { children: [/* @__PURE__ */ E("circle", {
		cx: "12",
		cy: "12",
		r: "10"
	}), /* @__PURE__ */ E("path", { d: "m4.9 4.9 14.2 14.2" })] }),
	check: () => /* @__PURE__ */ E("path", { d: "M20 6 9 17l-5-5" }),
	octagon: () => /* @__PURE__ */ E(e, { children: [
		/* @__PURE__ */ E("polygon", { points: "7.86 2 16.14 2 22 7.86 22 16.14 16.14 22 7.86 22 2 16.14 2 7.86" }),
		/* @__PURE__ */ E("line", {
			x1: "15",
			y1: "9",
			x2: "9",
			y2: "15"
		}),
		/* @__PURE__ */ E("line", {
			x1: "9",
			y1: "9",
			x2: "15",
			y2: "15"
		})
	] }),
	panel: () => /* @__PURE__ */ E(e, { children: [/* @__PURE__ */ E("rect", {
		x: "3",
		y: "3",
		width: "18",
		height: "18",
		rx: "2"
	}), /* @__PURE__ */ E("line", {
		x1: "9",
		y1: "3",
		x2: "9",
		y2: "21"
	})] }),
	search: () => /* @__PURE__ */ E(e, { children: [/* @__PURE__ */ E("circle", {
		cx: "11",
		cy: "11",
		r: "7"
	}), /* @__PURE__ */ E("line", {
		x1: "21",
		y1: "21",
		x2: "16.65",
		y2: "16.65"
	})] }),
	theme: () => /* @__PURE__ */ E("path", { d: "M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z" })
};
function k({ name: e, size: t = 16 }) {
	let n = O[e];
	return n ? /* @__PURE__ */ E("svg", {
		class: "icon",
		width: t,
		height: t,
		viewBox: "0 0 24 24",
		fill: "none",
		stroke: "currentColor",
		"stroke-width": "2",
		"stroke-linecap": "round",
		"stroke-linejoin": "round",
		"aria-hidden": "true",
		children: n()
	}) : null;
}
//#endregion
//#region views/StatCard.tsx
function A({ n: e, label: t, icon: n, tone: r }) {
	return /* @__PURE__ */ E("div", {
		class: "card" + (r ? " card--" + r : ""),
		children: [
			n && /* @__PURE__ */ E("span", {
				class: "card__icon",
				"aria-hidden": "true",
				children: /* @__PURE__ */ E(k, {
					name: n,
					size: 17
				})
			}),
			/* @__PURE__ */ E("div", {
				class: "card__num",
				children: e
			}),
			/* @__PURE__ */ E("div", {
				class: "card__label",
				children: t
			})
		]
	});
}
//#endregion
//#region views/SegmentedControl.tsx
function j({ value: e, options: t, onChange: n, ariaLabel: r }) {
	let i = Math.max(0, t.findIndex((t) => t.value === e));
	return /* @__PURE__ */ E("div", {
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
		children: t.map((t, r) => /* @__PURE__ */ E("button", {
			type: "button",
			role: "radio",
			"aria-checked": e === t.value,
			"data-tone": t.tone,
			tabIndex: r === i ? 0 : -1,
			class: "seg__opt" + (e === t.value ? " on" : ""),
			onClick: () => n(t.value),
			children: [t.label, t.badge != null && /* @__PURE__ */ E("span", {
				class: "seg__badge",
				"aria-hidden": "true",
				children: t.badge
			})]
		}))
	});
}
//#endregion
//#region views/DecisionBadge.tsx
function M({ decision: e }) {
	return /* @__PURE__ */ E("span", {
		class: "decision d-" + (e || ""),
		children: e || /* @__PURE__ */ E("span", {
			class: "faint",
			children: "—"
		})
	});
}
//#endregion
//#region views/IntegrityBadge.tsx
function N({ v: e, compact: t, href: n, onClick: r }) {
	let i = e == null ? "badge" : e.ok ? "badge ok" : "badge bad", a = e == null ? "Verifying…" : e.ok ? "Chain intact ✓" : `Chain broken @${e.brokenAt} ✗`, o = e == null ? "Checking the audit hash-chain…" : e.ok ? "Every audit event is hash-linked to the one before it, so any edit or deletion is detectable. Intact means nothing was tampered with. Click to open the audit trail." : `The audit hash-chain is broken at event #${e.brokenAt}: a row was altered or removed. Click to open the audit trail.`;
	return t ? /* @__PURE__ */ E("a", {
		class: i + " badge--icon",
		href: n,
		title: a,
		"aria-label": a,
		onClick: r,
		children: /* @__PURE__ */ E(k, {
			name: e && !e.ok ? "octagon" : "policies",
			size: 15
		})
	}) : /* @__PURE__ */ E("a", {
		class: i,
		href: n,
		"data-tip": o,
		"aria-label": a,
		onClick: r,
		children: a
	});
}
//#endregion
//#region views/IntegrityPanel.tsx
function P({ verify: e, checkedAt: t, recheck: n, count: r }) {
	let i = e == null ? "verifying" : e.ok ? "intact" : "broken", a = i === "verifying" ? "Verifying chain…" : i === "intact" ? "Audit chain intact" : `Chain broken at #${e.brokenAt}`;
	return /* @__PURE__ */ E("div", {
		class: "integrity integrity--" + i,
		children: [
			/* @__PURE__ */ E("span", {
				class: "integrity__icon",
				"aria-hidden": "true",
				children: /* @__PURE__ */ E(k, {
					name: i === "broken" ? "octagon" : "policies",
					size: 22
				})
			}),
			/* @__PURE__ */ E("div", {
				class: "integrity__body",
				children: [/* @__PURE__ */ E("div", {
					class: "integrity__status",
					children: a
				}), /* @__PURE__ */ E("p", {
					class: "integrity__desc",
					children: i === "broken" ? "An event was altered or removed after it was written — everything from that point on is suspect." : "Every event is hash-linked to the one before it, so any edit or deletion is detectable after the fact."
				})]
			}),
			/* @__PURE__ */ E("dl", {
				class: "integrity__meta",
				children: [/* @__PURE__ */ E("div", { children: [/* @__PURE__ */ E("dt", { children: "Events" }), /* @__PURE__ */ E("dd", { children: r })] }), /* @__PURE__ */ E("div", { children: [/* @__PURE__ */ E("dt", { children: "Last verified" }), /* @__PURE__ */ E("dd", { children: t ? v(new Date(t).toISOString()) : "…" })] })]
			}),
			/* @__PURE__ */ E("button", {
				type: "button",
				class: "btn btn--ghost btn--sm integrity__btn",
				onClick: n,
				children: "Re-verify"
			})
		]
	});
}
//#endregion
//#region views/PoddleMark.tsx
function F({ size: e = 30 }) {
	return /* @__PURE__ */ E("svg", {
		class: "pmark",
		width: e,
		height: e,
		viewBox: "382.0 134.1 435.9 435.9",
		"aria-hidden": "true",
		children: [
			/* @__PURE__ */ E("path", {
				d: "M769.71,450.00 L769.71,254.04 L600.00,352.02 L600.00,547.98 Z",
				fill: "currentColor"
			}),
			/* @__PURE__ */ E("path", {
				d: "M600.00,547.98 L600.00,352.02 L430.29,254.04 L430.29,450.00 Z",
				fill: "currentColor"
			}),
			/* @__PURE__ */ E("path", {
				d: "M769.71,254.04 L600.00,156.06 L430.29,254.04 L600.00,352.02 Z",
				fill: "#2f9e6f"
			}),
			/* @__PURE__ */ E("g", {
				transform: "matrix(169.7056,97.9787,0.0000,195.9601,430.29,254.04)",
				fill: "#2f9e6f",
				children: [/* @__PURE__ */ E("path", { d: "M0.19,0.31 L0.29,0.31 L0.44,0.50 L0.29,0.69 L0.19,0.69 L0.34,0.50 Z" }), /* @__PURE__ */ E("path", { d: "M0.50,0.605 L0.72,0.605 L0.72,0.685 L0.50,0.685 Z" })]
			})
		]
	});
}
//#endregion
//#region views/EgressChart.tsx
function I({ events: e }) {
	let [t, n] = i(null), a = r(() => w(e, 14), [e]);
	if (a.length === 0) return /* @__PURE__ */ E("div", {
		class: "chart-empty",
		children: "No egress yet. Requests chart here as your agents run."
	});
	let o = a.length, s = Math.max(1, ...a.map((e) => e.req)), c = 984 / o, l = Math.min(46, c * .6), u = (e) => 8 + (e + .5) * c, d = (e) => e / s * 136, f = a.reduce((e, t) => e + t.req, 0), p = a.reduce((e, t) => e + t.intervened, 0), m = t == null ? null : a[t];
	return /* @__PURE__ */ E("div", {
		class: "chart",
		children: [/* @__PURE__ */ E("svg", {
			class: "plot",
			viewBox: "0 0 1000 172",
			preserveAspectRatio: "xMidYMid meet",
			role: "img",
			"aria-label": `Egress over time: ${f} requests, ${p} redacted or blocked, across ${o} intervals`,
			children: [
				/* @__PURE__ */ E("line", {
					class: "grid grid--soft",
					x1: 8,
					y1: 14,
					x2: 992,
					y2: 14,
					"vector-effect": "non-scaling-stroke"
				}),
				/* @__PURE__ */ E("text", {
					class: "axtick",
					x: 8,
					y: 10,
					children: s
				}),
				/* @__PURE__ */ E("line", {
					class: "grid",
					x1: 8,
					y1: 150,
					x2: 992,
					y2: 150,
					"vector-effect": "non-scaling-stroke"
				}),
				a.map((e, r) => {
					let i = e.req - e.intervened, a = d(i), o = d(e.intervened), s = u(r) - l / 2, f = t != null && t !== r ? " bar--dim" : "", p = e.intervened > 0 && i > 0 ? 2 : 0;
					return /* @__PURE__ */ E("g", { children: [
						i > 0 && /* @__PURE__ */ E("rect", {
							class: "bar bar--allow" + f,
							x: s,
							y: 150 - a,
							width: l,
							height: a,
							rx: "3"
						}),
						e.intervened > 0 && /* @__PURE__ */ E("rect", {
							class: "bar bar--int" + f,
							x: s,
							y: 150 - a - p - o,
							width: l,
							height: o,
							rx: "3"
						}),
						/* @__PURE__ */ E("rect", {
							x: u(r) - c / 2,
							y: 14,
							width: c,
							height: 136,
							fill: "transparent",
							onMouseEnter: () => n(r),
							onMouseLeave: () => n(null)
						})
					] }, r);
				}),
				/* @__PURE__ */ E("text", {
					class: "axlabel",
					x: 8,
					y: 166,
					"text-anchor": "start",
					children: v(new Date(a[0].t0).toISOString())
				}),
				/* @__PURE__ */ E("text", {
					class: "axlabel",
					x: 992,
					y: 166,
					"text-anchor": "end",
					children: "now"
				})
			]
		}), m && /* @__PURE__ */ E("div", {
			class: "tip",
			style: `left:${((t + .5) / o * 100).toFixed(2)}%`,
			"aria-hidden": "true",
			children: [
				/* @__PURE__ */ E("div", {
					class: "tip__t",
					children: [
						v(new Date(m.t0).toISOString()),
						" · ",
						m.req,
						" total"
					]
				}),
				/* @__PURE__ */ E("div", {
					class: "tip__row",
					children: [/* @__PURE__ */ E("span", {
						class: "tip__k",
						children: [/* @__PURE__ */ E("span", { class: "dotmark dotmark--req" }), "Allowed"]
					}), /* @__PURE__ */ E("span", {
						class: "tip__v",
						children: m.req - m.intervened
					})]
				}),
				/* @__PURE__ */ E("div", {
					class: "tip__row",
					children: [/* @__PURE__ */ E("span", {
						class: "tip__k",
						children: [/* @__PURE__ */ E("span", { class: "dotmark dotmark--int" }), "Intervened"]
					}), /* @__PURE__ */ E("span", {
						class: "tip__v",
						children: m.intervened
					})]
				})
			]
		})]
	});
}
//#endregion
//#region views/PostureBar.tsx
function L({ counts: e }) {
	let t = b.reduce((t, n) => t + (e[n.key] || 0), 0);
	if (t === 0) return /* @__PURE__ */ E("div", {
		class: "chart-empty",
		children: "No decisions recorded yet."
	});
	let n = (e) => Math.round(e / t * 100);
	return /* @__PURE__ */ E("div", {
		class: "posture",
		children: [/* @__PURE__ */ E("div", {
			class: "posture__bar",
			role: "img",
			"aria-label": "Decision mix: " + b.map((t) => `${e[t.key] || 0} ${t.label}`).join(", "),
			children: b.filter((t) => (e[t.key] || 0) > 0).map((t) => /* @__PURE__ */ E("div", {
				class: "posture__seg d-" + t.key,
				style: `flex-grow:${e[t.key]}`,
				title: `${t.label}: ${e[t.key]} (${n(e[t.key])}%)`
			}, t.key))
		}), /* @__PURE__ */ E("ul", {
			class: "legend",
			children: b.map((t) => /* @__PURE__ */ E("li", {
				class: "legend__i",
				children: [
					/* @__PURE__ */ E("span", {
						class: "legend__mk d-" + t.key,
						children: /* @__PURE__ */ E(k, {
							name: t.icon,
							size: 13
						})
					}),
					/* @__PURE__ */ E("span", {
						class: "legend__lb",
						children: t.label
					}),
					/* @__PURE__ */ E("span", {
						class: "legend__v",
						children: e[t.key] || 0
					}),
					/* @__PURE__ */ E("span", {
						class: "legend__pc",
						children: [n(e[t.key] || 0), "%"]
					})
				]
			}, t.key))
		})]
	});
}
//#endregion
//#region views/FleetLoad.tsx
function R({ pods: e }) {
	let t = e.filter((e) => e.state === "running");
	return t.length === 0 ? /* @__PURE__ */ E("div", {
		class: "chart-empty",
		children: "No pods running right now."
	}) : /* @__PURE__ */ E("div", {
		class: "fleet",
		children: t.map((e) => {
			let t = parseFloat(e.cpu) || 0;
			return /* @__PURE__ */ E("div", {
				class: "fleet__row",
				title: `${e.name}: CPU ${e.cpu}, memory ${e.memPerc}`,
				children: [
					/* @__PURE__ */ E("span", {
						class: "fleet__name",
						children: e.name
					}),
					/* @__PURE__ */ E("span", {
						class: "fleet__track",
						"aria-hidden": "true",
						children: /* @__PURE__ */ E("span", {
							class: "fleet__fill fleet__fill--" + y(t),
							style: `width:${Math.min(100, t)}%`
						})
					}),
					/* @__PURE__ */ E("span", {
						class: "fleet__val c-mono",
						children: e.cpu || "—"
					}),
					/* @__PURE__ */ E("span", {
						class: "fleet__mem c-mono",
						title: "memory in use",
						children: e.memPerc || "—"
					})
				]
			}, e.name);
		})
	});
}
//#endregion
//#region views/MixBar.tsx
function z({ d: e }) {
	let t = [
		["allow", e.allow],
		["redact", e.redact],
		["deny", e.deny],
		["block", e.block]
	].filter(([, e]) => e > 0);
	return /* @__PURE__ */ E("span", {
		class: "mix",
		role: "img",
		"aria-label": t.map(([e, t]) => `${t} ${e}`).join(", "),
		children: t.map(([e, t]) => /* @__PURE__ */ E("span", {
			class: "mix__seg d-" + e,
			style: `flex-grow:${t}`,
			title: `${e}: ${t}`
		}, e))
	});
}
//#endregion
//#region views/Skeletons.tsx
function B() {
	return /* @__PURE__ */ E("div", {
		class: "cards",
		"aria-hidden": "true",
		children: [
			0,
			1,
			2,
			3
		].map((e) => /* @__PURE__ */ E("div", {
			class: "card",
			children: [/* @__PURE__ */ E("span", { class: "skel skel--num" }), /* @__PURE__ */ E("span", { class: "skel skel--sm" })]
		}, e))
	});
}
function V({ rows: e = 6 }) {
	return /* @__PURE__ */ E("div", {
		class: "table-wrap skel-table",
		"aria-hidden": "true",
		"aria-busy": "true",
		children: Array.from({ length: e }).map((e, t) => /* @__PURE__ */ E("div", {
			class: "skel-tr",
			children: /* @__PURE__ */ E("span", { class: "skel" })
		}, t))
	});
}
//#endregion
//#region views/LiveDot.tsx
function H({ status: e }) {
	let t = e === "live" ? "Live" : e === "down" ? "Reconnecting" : "Connecting";
	return /* @__PURE__ */ E("span", {
		class: "live live--" + e,
		title: "Audit stream: " + t,
		role: "status",
		children: [/* @__PURE__ */ E("span", {
			class: "live__dot",
			"aria-hidden": "true"
		}), t]
	});
}
//#endregion
//#region views/Fact.tsx
function U({ label: e, children: t }) {
	return /* @__PURE__ */ E("div", { children: [/* @__PURE__ */ E("dt", { children: e }), /* @__PURE__ */ E("dd", { children: t })] });
}
//#endregion
//#region views/csv.ts
function W(e) {
	let t = [
		"seq",
		"time",
		"pod",
		"kind",
		"decision",
		"upstream",
		"method",
		"status",
		"detail"
	], n = (e) => {
		let t = String(e ?? "");
		return /[",\n]/.test(t) ? "\"" + t.replace(/"/g, "\"\"") + "\"" : t;
	}, r = e.map((e) => t.map((t) => n(e[t])).join(","));
	return [t.join(","), ...r].join("\n");
}
function G(e, t) {
	let n = new Blob([W(t)], { type: "text/csv;charset=utf-8" }), r = URL.createObjectURL(n), i = document.createElement("a");
	i.href = r, i.download = e, document.body.appendChild(i), i.click(), i.remove(), setTimeout(() => URL.revokeObjectURL(r), 0);
}
//#endregion
//#region views/AuditLogTable.tsx
var K = [
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
function q({ events: e, initialPod: t, initialQ: a, loading: c, onExport: l }) {
	let [u, d] = i(t || a || ""), [f, p] = i(""), [m, h] = i("");
	n(() => {
		t ? d(t) : a && d(a);
	}, [t, a]);
	let y = r(() => {
		let t = m && s[m] ? Date.now() - s[m] : 0, n = u.toLowerCase();
		return e.filter((e) => t && new Date(e.time).getTime() < t ? !1 : !u || (e.pod || "").toLowerCase().includes(n) || (e.kind || "").toLowerCase().includes(n) || (e.upstream || "").toLowerCase().includes(n));
	}, [
		e,
		u,
		m
	]), b = r(() => {
		let e = {
			"": y.length,
			allow: 0,
			redact: 0,
			block: 0,
			deny: 0
		};
		for (let t of y) t.decision && t.decision in e && e[t.decision]++;
		return e;
	}, [y]), x = r(() => f ? y.filter((e) => e.decision === f) : y, [y, f]), S = K.map((e) => ({
		...e,
		badge: b[e.value] ?? 0
	})), C = /* @__PURE__ */ E("div", {
		class: "toolbar",
		children: [
			/* @__PURE__ */ E("input", {
				class: "grow",
				"aria-label": "Filter events by pod, kind, or upstream",
				placeholder: "Filter by pod, kind, or upstream…",
				value: u,
				onInput: (e) => d(e.target.value)
			}),
			/* @__PURE__ */ E(j, {
				value: m,
				options: o,
				onChange: h,
				ariaLabel: "time range"
			}),
			/* @__PURE__ */ E(j, {
				value: f,
				options: S,
				onChange: p,
				ariaLabel: "filter by decision"
			}),
			/* @__PURE__ */ E("button", {
				type: "button",
				class: "btn btn--ghost btn--sm",
				disabled: !x.length,
				onClick: () => {
					G("poddle-audit.csv", x), l?.(x);
				},
				children: "Export CSV"
			}),
			/* @__PURE__ */ E("span", {
				class: "count",
				children: [x.length, " events"]
			})
		]
	});
	return c ? /* @__PURE__ */ E("div", { children: [C, /* @__PURE__ */ E(V, { rows: 8 })] }) : /* @__PURE__ */ E("div", { children: [C, /* @__PURE__ */ E("div", {
		class: "table-wrap",
		children: /* @__PURE__ */ E("table", {
			class: "dense",
			children: [/* @__PURE__ */ E("thead", { children: /* @__PURE__ */ E("tr", { children: [
				/* @__PURE__ */ E("th", {
					scope: "col",
					children: "time"
				}),
				/* @__PURE__ */ E("th", {
					scope: "col",
					children: "pod"
				}),
				/* @__PURE__ */ E("th", {
					scope: "col",
					children: "kind"
				}),
				/* @__PURE__ */ E("th", {
					scope: "col",
					children: "decision"
				}),
				/* @__PURE__ */ E("th", {
					scope: "col",
					children: "upstream"
				}),
				/* @__PURE__ */ E("th", {
					scope: "col",
					children: "detail"
				})
			] }) }), /* @__PURE__ */ E("tbody", { children: [x.length === 0 && /* @__PURE__ */ E("tr", { children: /* @__PURE__ */ E("td", {
				colSpan: 6,
				class: "empty",
				children: u || f || m ? "No events match your filter." : "Monitoring active — no events recorded yet."
			}) }), x.slice(0, 800).map((e) => /* @__PURE__ */ E("tr", {
				class: "auditrow",
				children: [
					/* @__PURE__ */ E("td", {
						class: "c-time",
						title: new Date(e.time).toLocaleString(),
						children: v(e.time)
					}),
					/* @__PURE__ */ E("td", {
						class: "c-pod",
						children: e.pod || /* @__PURE__ */ E("span", {
							class: "faint",
							children: "—"
						})
					}),
					/* @__PURE__ */ E("td", { children: _(e.kind) }),
					/* @__PURE__ */ E("td", { children: /* @__PURE__ */ E(M, { decision: e.decision }) }),
					/* @__PURE__ */ E("td", {
						class: "c-mono",
						children: e.upstream || /* @__PURE__ */ E("span", {
							class: "faint",
							children: "—"
						})
					}),
					/* @__PURE__ */ E("td", {
						class: "c-detail",
						children: g(e.detail || "")
					})
				]
			}, e.seq))] })]
		})
	})] });
}
//#endregion
//#region views/PodFleetTable.tsx
function J({ pods: e, hist: t, onPod: n, emptyState: r }) {
	return /* @__PURE__ */ E("div", {
		class: "table-wrap",
		children: /* @__PURE__ */ E("table", { children: [/* @__PURE__ */ E("thead", { children: /* @__PURE__ */ E("tr", { children: [
			/* @__PURE__ */ E("th", {
				scope: "col",
				children: "pod"
			}),
			/* @__PURE__ */ E("th", {
				scope: "col",
				children: "state"
			}),
			/* @__PURE__ */ E("th", {
				scope: "col",
				children: "size"
			}),
			/* @__PURE__ */ E("th", {
				scope: "col",
				children: "mode"
			}),
			/* @__PURE__ */ E("th", {
				scope: "col",
				children: "policy"
			}),
			/* @__PURE__ */ E("th", {
				scope: "col",
				class: "num",
				children: "cpu"
			}),
			/* @__PURE__ */ E("th", {
				scope: "col",
				class: "num",
				children: "memory"
			})
		] }) }), /* @__PURE__ */ E("tbody", { children: [e.length === 0 && /* @__PURE__ */ E("tr", { children: /* @__PURE__ */ E("td", {
			colSpan: 7,
			class: "empty",
			children: r
		}) }), e.map((e) => {
			let r = t[e.name] || {
				cpu: [],
				mem: []
			};
			return /* @__PURE__ */ E("tr", {
				class: "clickable",
				tabIndex: 0,
				onClick: () => n(e.name),
				onKeyDown: C(() => n(e.name)),
				children: [
					/* @__PURE__ */ E("td", {
						class: "c-pod",
						children: [e.name, e.autoscale && /* @__PURE__ */ E("span", {
							class: "tag",
							children: "auto"
						})]
					}),
					/* @__PURE__ */ E("td", { children: /* @__PURE__ */ E("span", {
						class: "state state--" + e.state,
						children: e.state
					}) }),
					/* @__PURE__ */ E("td", {
						class: "c-mono",
						children: g(e.size)
					}),
					/* @__PURE__ */ E("td", {
						class: "c-mono",
						children: e.mode ? g(e.mode) : /* @__PURE__ */ E("span", {
							class: "faint",
							children: "—"
						})
					}),
					/* @__PURE__ */ E("td", {
						class: "c-mono",
						children: e.policy || /* @__PURE__ */ E("span", {
							class: "faint",
							children: "—"
						})
					}),
					/* @__PURE__ */ E("td", {
						class: "perf",
						children: [/* @__PURE__ */ E(D, { data: r.cpu }), /* @__PURE__ */ E("span", {
							class: "c-mono",
							children: e.cpu || "—"
						})]
					}),
					/* @__PURE__ */ E("td", {
						class: "perf",
						children: [/* @__PURE__ */ E(D, { data: r.mem }), /* @__PURE__ */ E("span", {
							class: "c-mono",
							children: e.memPerc || "—"
						})]
					})
				]
			}, e.name);
		})] })] })
	});
}
//#endregion
//#region views/PodDetailPanel.tsx
function Y({ name: t, pod: n, hist: r, events: i, loading: a, backHref: o, onBack: s, policyHref: c, onPolicyClick: l, controls: u }) {
	return /* @__PURE__ */ E("div", { children: [
		/* @__PURE__ */ E("div", {
			class: "detail-head",
			children: [
				/* @__PURE__ */ E("a", {
					href: o,
					class: "back",
					onClick: s,
					children: "← Pods"
				}),
				/* @__PURE__ */ E("h1", {
					class: "detail-title",
					children: t
				}),
				n ? /* @__PURE__ */ E("span", {
					class: "state state--" + n.state,
					children: n.state
				}) : /* @__PURE__ */ E("span", {
					class: "state state--stopped",
					children: "not running"
				}),
				n?.autoscale && /* @__PURE__ */ E("span", {
					class: "tag",
					children: "auto"
				})
			]
		}),
		n && /* @__PURE__ */ E("dl", {
			class: "facts",
			children: [
				/* @__PURE__ */ E(U, {
					label: "size",
					children: /* @__PURE__ */ E("span", {
						class: "c-mono",
						children: g(n.size)
					})
				}),
				/* @__PURE__ */ E(U, {
					label: "mode",
					children: /* @__PURE__ */ E("span", {
						class: "c-mono",
						children: n.mode ? g(n.mode) : "—"
					})
				}),
				/* @__PURE__ */ E(U, {
					label: "policy",
					children: n.policy ? /* @__PURE__ */ E("a", {
						class: "fact-link c-mono",
						href: c,
						onClick: l,
						children: n.policy
					}) : /* @__PURE__ */ E("span", {
						class: "faint",
						children: "none"
					})
				}),
				/* @__PURE__ */ E(U, {
					label: "cpu",
					children: /* @__PURE__ */ E("span", {
						class: "perf-inline",
						children: [/* @__PURE__ */ E(D, { data: r.cpu }), /* @__PURE__ */ E("span", {
							class: "c-mono",
							children: n.cpu || "—"
						})]
					})
				}),
				/* @__PURE__ */ E(U, {
					label: "memory",
					children: /* @__PURE__ */ E("span", {
						class: "perf-inline",
						children: [/* @__PURE__ */ E(D, { data: r.mem }), /* @__PURE__ */ E("span", {
							class: "c-mono",
							children: n.mem || "—"
						})]
					})
				})
			]
		}),
		u && /* @__PURE__ */ E(e, { children: [/* @__PURE__ */ E("h2", {
			class: "section-title",
			children: "Controls"
		}), u] }),
		/* @__PURE__ */ E("h2", {
			class: "section-title",
			children: "Audit trail"
		}),
		/* @__PURE__ */ E(q, {
			events: i,
			initialPod: t,
			loading: a
		})
	] });
}
//#endregion
//#region views/OverviewCards.tsx
function X({ stats: e }) {
	return /* @__PURE__ */ E("div", {
		class: "cards",
		children: [
			/* @__PURE__ */ E(A, {
				n: e.pods,
				label: "pods active",
				icon: "pods"
			}),
			/* @__PURE__ */ E(A, {
				n: e.requests,
				label: "requests",
				icon: "globe"
			}),
			/* @__PURE__ */ E(A, {
				n: e.secrets,
				label: "secrets redacted",
				icon: "eyeoff",
				tone: e.secrets ? "warn" : void 0
			}),
			/* @__PURE__ */ E(A, {
				n: e.blocked + e.denied,
				label: "blocked / denied",
				icon: "ban",
				tone: e.blocked + e.denied ? "flag" : void 0
			})
		]
	});
}
//#endregion
//#region views/AttentionPanel.tsx
function Z({ attention: t, onPod: n }) {
	return /* @__PURE__ */ E(e, { children: [/* @__PURE__ */ E("h2", {
		class: "section-title",
		children: "Attention"
	}), t.length === 0 ? /* @__PURE__ */ E("div", {
		class: "panel empty",
		children: "No policy denials or blocks — agents are inside their guardrails."
	}) : /* @__PURE__ */ E("div", {
		class: "panel",
		children: t.map((e) => /* @__PURE__ */ E("button", {
			class: "attn",
			onClick: () => n(e.pod),
			children: [
				/* @__PURE__ */ E("span", {
					class: "attn__pod",
					children: e.pod
				}),
				/* @__PURE__ */ E("span", {
					class: "attn__desc",
					children: [
						/* @__PURE__ */ E(M, { decision: e.decision }),
						" ",
						e.upstream
					]
				}),
				/* @__PURE__ */ E("span", {
					class: "attn__count",
					children: ["×", e.count]
				})
			]
		}))
	})] });
}
//#endregion
//#region views/RedactionsTable.tsx
function Q({ redactions: t, onPod: n }) {
	return /* @__PURE__ */ E(e, { children: [/* @__PURE__ */ E("h2", {
		class: "section-title",
		children: "Secrets redacted"
	}), t.length === 0 ? /* @__PURE__ */ E("div", {
		class: "panel empty",
		children: "No secrets redacted yet — redact-mode policies strip credentials the agent tries to send."
	}) : /* @__PURE__ */ E("div", {
		class: "table-wrap",
		children: /* @__PURE__ */ E("table", { children: [/* @__PURE__ */ E("thead", { children: /* @__PURE__ */ E("tr", { children: [
			/* @__PURE__ */ E("th", { children: "pod" }),
			/* @__PURE__ */ E("th", { children: "destination" }),
			/* @__PURE__ */ E("th", { children: "secrets" }),
			/* @__PURE__ */ E("th", { children: "times" })
		] }) }), /* @__PURE__ */ E("tbody", { children: t.map((e) => /* @__PURE__ */ E("tr", {
			class: "clickable",
			tabIndex: 0,
			onClick: () => n(e.pod),
			onKeyDown: C(() => n(e.pod)),
			children: [
				/* @__PURE__ */ E("td", {
					class: "c-pod",
					children: e.pod
				}),
				/* @__PURE__ */ E("td", {
					class: "c-mono",
					children: e.upstream
				}),
				/* @__PURE__ */ E("td", {
					class: "c-mono",
					children: e.secrets
				}),
				/* @__PURE__ */ E("td", {
					class: "c-mono",
					children: ["×", e.count]
				})
			]
		})) })] })
	})] });
}
//#endregion
//#region views/PolicyList.tsx
function $({ policies: e, selectedName: t, loading: n, usage: r, hrefFor: i, newHref: a, linkTo: o }) {
	return /* @__PURE__ */ E("div", {
		class: "list",
		children: [n ? [
			0,
			1,
			2
		].map((e) => /* @__PURE__ */ E("span", {
			class: "list__skel skel",
			"aria-hidden": "true"
		}, e)) : e.map((e) => {
			let n = r(e.name);
			return /* @__PURE__ */ E("a", {
				href: i(e.name),
				onClick: o(i(e.name)),
				class: "list__row" + (t === e.name ? " on" : ""),
				children: [/* @__PURE__ */ E("span", { children: e.name }), n > 0 && /* @__PURE__ */ E("span", {
					class: "list__meta",
					title: `${n} running pod${n === 1 ? "" : "s"} use this policy`,
					children: [
						n,
						" pod",
						n === 1 ? "" : "s"
					]
				})]
			}, e.name);
		}), /* @__PURE__ */ E("a", {
			href: a,
			onClick: o(a),
			class: "new",
			children: "＋ New policy"
		})]
	});
}
//#endregion
//#region views/DestinationsTable.tsx
function ee({ dests: e, loading: t, onSelect: n }) {
	return t ? /* @__PURE__ */ E(V, { rows: 6 }) : /* @__PURE__ */ E("div", {
		class: "table-wrap",
		children: /* @__PURE__ */ E("table", { children: [/* @__PURE__ */ E("thead", { children: /* @__PURE__ */ E("tr", { children: [
			/* @__PURE__ */ E("th", {
				scope: "col",
				children: "destination"
			}),
			/* @__PURE__ */ E("th", {
				scope: "col",
				class: "num",
				children: "requests"
			}),
			/* @__PURE__ */ E("th", {
				scope: "col",
				children: "decision mix"
			}),
			/* @__PURE__ */ E("th", {
				scope: "col",
				class: "num",
				children: "pods"
			}),
			/* @__PURE__ */ E("th", {
				scope: "col",
				class: "num",
				children: "secrets"
			})
		] }) }), /* @__PURE__ */ E("tbody", { children: e.map((e) => /* @__PURE__ */ E("tr", {
			class: "clickable",
			tabIndex: 0,
			onClick: () => n(e.upstream),
			onKeyDown: C(() => n(e.upstream)),
			children: [
				/* @__PURE__ */ E("td", {
					class: "c-mono dest__host",
					children: [e.upstream, (e.deny || e.block) > 0 && /* @__PURE__ */ E("span", {
						class: "dest__flag",
						"aria-hidden": "true",
						title: "denied or blocked here",
						children: /* @__PURE__ */ E(k, {
							name: "ban",
							size: 12
						})
					})]
				}),
				/* @__PURE__ */ E("td", {
					class: "num c-mono",
					children: e.total
				}),
				/* @__PURE__ */ E("td", { children: /* @__PURE__ */ E(z, { d: e }) }),
				/* @__PURE__ */ E("td", {
					class: "num c-mono",
					title: [...e.pods].join(", "),
					children: e.pods.size
				}),
				/* @__PURE__ */ E("td", {
					class: "num c-mono",
					children: e.secrets || /* @__PURE__ */ E("span", {
						class: "faint",
						children: "—"
					})
				})
			]
		}, e.upstream)) })] })
	});
}
//#endregion
//#region views/PolicyEditor.tsx
var te = [
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
		value: "off",
		label: "Off",
		tone: "faint"
	}
];
function ne({ policy: e, events: t, scopePods: o, onSave: s, onDelete: c, hint: l }) {
	let [u, p] = i(e.name), [m, h] = i(() => f(e)), [g, _] = i(e.deny_upstreams || []), [v, y] = i(e.egress || "redact"), [b, x] = i("");
	n(() => {
		p(e.name), h(f(e)), _(e.deny_upstreams || []), y(e.egress || "redact"), x("");
	}, [e]);
	let S = (e, t) => h((n) => n.map((n, r) => r === e ? {
		...n,
		...t
	} : n)), C = (e, t) => h((n) => n.map((n, r) => r === e ? {
		...n,
		methods: n.methods.includes(t) ? n.methods.filter((e) => e !== t) : [...n.methods, t]
	} : n)), w = () => h((e) => [...e, {
		host: "",
		methods: [],
		open: !1
	}]), T = (e) => h((t) => t.filter((t, n) => n !== e)), D = (e, t) => _((n) => n.map((n, r) => r === e ? t : n)), O = () => _((e) => [...e, ""]), A = (e) => _((t) => t.filter((t, n) => n !== e)), N = () => {
		let e = m.map((e) => e.host.trim()).filter(Boolean), t = g.map((e) => e.trim()).filter(Boolean), n = {};
		for (let e of m) {
			let t = e.host.trim();
			t && e.methods.length && (n[t] = e.methods);
		}
		return {
			name: u.trim(),
			allow_upstreams: e,
			deny_upstreams: t,
			methods: n,
			egress: v
		};
	}, P = o.length > 0, F = r(() => P ? t.filter((e) => e.pod && o.includes(e.pod)) : t, [
		t,
		o,
		P
	]), I = r(() => d(N(), F), [
		u,
		m,
		g,
		v,
		F
	]);
	return /* @__PURE__ */ E("div", {
		class: "editor",
		children: [
			/* @__PURE__ */ E("div", {
				class: "row",
				children: [/* @__PURE__ */ E("div", { children: [/* @__PURE__ */ E("label", {
					for: "pol-name",
					children: "Name"
				}), /* @__PURE__ */ E("input", {
					id: "pol-name",
					value: u,
					onInput: (e) => p(e.target.value)
				})] }), /* @__PURE__ */ E("div", {
					class: "narrow",
					children: [/* @__PURE__ */ E("label", { children: "Egress mode" }), /* @__PURE__ */ E(j, {
						value: v,
						options: te,
						onChange: y,
						ariaLabel: "egress mode"
					})]
				})]
			}),
			/* @__PURE__ */ E("label", { children: ["Allowed destinations ", /* @__PURE__ */ E("span", {
				class: "label-hint",
				children: "Default-deny once any are set · \".example.com\" matches any subdomain"
			})] }),
			/* @__PURE__ */ E("div", {
				class: "rules",
				children: [
					m.length === 0 && /* @__PURE__ */ E("p", {
						class: "rules__empty",
						children: "No destinations yet — every host is allowed, subject to the blocked list and egress mode."
					}),
					m.map((e, t) => /* @__PURE__ */ E("div", {
						class: "rule",
						children: [/* @__PURE__ */ E("div", {
							class: "rule__row",
							children: [
								/* @__PURE__ */ E("input", {
									class: "rule__host",
									value: e.host,
									placeholder: "api.example.com",
									"aria-label": "Allowed host",
									onInput: (e) => S(t, { host: e.target.value })
								}),
								!e.open && (e.methods.length ? /* @__PURE__ */ E("button", {
									type: "button",
									class: "rule__msum",
									title: "Limited to " + e.methods.join(", ") + " — click to edit",
									onClick: () => S(t, { open: !0 }),
									children: e.methods.length > 3 ? e.methods.length + " methods" : e.methods.join(", ")
								}) : /* @__PURE__ */ E("button", {
									type: "button",
									class: "rule__limit",
									onClick: () => S(t, { open: !0 }),
									children: "＋ limit methods"
								})),
								/* @__PURE__ */ E("button", {
									type: "button",
									class: "rule__rm",
									"aria-label": "Remove destination",
									onClick: () => T(t),
									children: "×"
								})
							]
						}), e.open && /* @__PURE__ */ E("div", {
							class: "rule__methods",
							children: [
								/* @__PURE__ */ E("span", {
									class: "rule__mlabel",
									children: "Allow only:"
								}),
								a.map((n) => /* @__PURE__ */ E("button", {
									type: "button",
									class: "mchip" + (e.methods.includes(n) ? " on" : ""),
									"aria-pressed": e.methods.includes(n),
									onClick: () => C(t, n),
									children: n
								}, n)),
								/* @__PURE__ */ E("button", {
									type: "button",
									class: "rule__mdone",
									onClick: () => S(t, { open: !1 }),
									children: "Done"
								}),
								e.methods.length > 0 && /* @__PURE__ */ E("button", {
									type: "button",
									class: "rule__mclear",
									onClick: () => S(t, {
										methods: [],
										open: !1
									}),
									children: "Clear"
								})
							]
						})]
					}, t)),
					/* @__PURE__ */ E("button", {
						type: "button",
						class: "addrow",
						onClick: w,
						children: "＋ Add destination"
					})
				]
			}),
			/* @__PURE__ */ E("label", { children: ["Always blocked ", /* @__PURE__ */ E("span", {
				class: "label-hint",
				children: "Wins over the allow-list"
			})] }),
			/* @__PURE__ */ E("div", {
				class: "rules",
				children: [g.map((e, t) => /* @__PURE__ */ E("div", {
					class: "rule",
					children: /* @__PURE__ */ E("div", {
						class: "rule__row",
						children: [/* @__PURE__ */ E("input", {
							class: "rule__host",
							value: e,
							placeholder: "metadata.google.internal",
							"aria-label": "Blocked host",
							onInput: (e) => D(t, e.target.value)
						}), /* @__PURE__ */ E("button", {
							type: "button",
							class: "rule__rm",
							"aria-label": "Remove blocked host",
							onClick: () => A(t),
							children: "×"
						})]
					})
				}, t)), /* @__PURE__ */ E("button", {
					type: "button",
					class: "addrow",
					onClick: O,
					children: "＋ Add blocked host"
				})]
			}),
			/* @__PURE__ */ E("div", {
				class: "dryrun",
				children: [
					/* @__PURE__ */ E("div", {
						class: "dryrun__head",
						children: [/* @__PURE__ */ E("span", {
							class: "dryrun__title",
							children: ["Dry-run · ", P ? `${o.length} pod${o.length === 1 ? "" : "s"} on this policy` : "all recent egress"]
						}), /* @__PURE__ */ E("span", {
							class: "dryrun__stat",
							children: [
								I.total,
								" request",
								I.total === 1 ? "" : "s",
								" ·",
								" ",
								/* @__PURE__ */ E("span", {
									class: I.denied ? "dryrun__deny" : "dryrun__ok",
									children: [I.denied, " would be denied"]
								})
							]
						})]
					}),
					I.total === 0 ? /* @__PURE__ */ E("div", {
						class: "dryrun__empty",
						children: P ? "The pods on this policy have no recent egress to evaluate." : "No recent egress to evaluate yet."
					}) : I.denied === 0 ? /* @__PURE__ */ E("div", {
						class: "dryrun__pass",
						children: [/* @__PURE__ */ E(k, {
							name: "check",
							size: 14
						}), " Every request passes these rules."]
					}) : /* @__PURE__ */ E("ul", {
						class: "dryrun__list",
						children: [I.rows.slice(0, 8).map((e) => /* @__PURE__ */ E("li", { children: [
							/* @__PURE__ */ E(M, { decision: "deny" }),
							/* @__PURE__ */ E("span", {
								class: "c-mono dryrun__dest",
								children: [e.method ? e.method + " " : "", e.upstream]
							}),
							/* @__PURE__ */ E("span", {
								class: "dryrun__reason",
								children: e.reason
							}),
							/* @__PURE__ */ E("span", {
								class: "dryrun__n",
								children: ["×", e.count]
							})
						] }, e.method + e.upstream)), I.rows.length > 8 && /* @__PURE__ */ E("li", {
							class: "dryrun__more",
							children: [
								"+",
								I.rows.length - 8,
								" more destinations"
							]
						})]
					}),
					/* @__PURE__ */ E("p", {
						class: "dryrun__note",
						children: [
							P ? "Replays these rules over the recent requests made by the pods that run this policy." : "No pods run this policy yet — previewed against all recent egress.",
							" ",
							"Evaluates allow/deny and method rules; secret redaction depends on request contents and is not simulated."
						]
					})
				]
			}),
			b && /* @__PURE__ */ E("div", {
				class: "err",
				children: b
			}),
			/* @__PURE__ */ E("div", {
				class: "actions",
				children: [/* @__PURE__ */ E("button", {
					class: "btn btn--primary",
					onClick: async () => {
						if (!u.trim()) {
							x("Name is required.");
							return;
						}
						let e = await s(N());
						e.ok || x(e.error || "Save failed");
					},
					children: "Save"
				}), e.name && /* @__PURE__ */ E("button", {
					class: "btn btn--danger",
					onClick: async () => {
						await c();
					},
					children: "Delete"
				})]
			}),
			l ? l(u) : /* @__PURE__ */ E("div", {
				class: "hint",
				children: [
					"Reference from a pod: ",
					/* @__PURE__ */ E("code", { children: ["poddle up --policy ", u || "<name>"] }),
					", or ",
					/* @__PURE__ */ E("code", { children: [
						"policy = \"",
						u || "<name>",
						"\""
					] }),
					" in a template."
				]
			})
		]
	});
}
//#endregion
//#region views/PodControls.tsx
function re({ pod: t, policies: n, onBind: r, onRevoke: a }) {
	let [o, s] = i(null), [c, l] = i(!1), [u, d] = i(null), f = async (e) => {
		l(!0);
		let t = await r(e);
		l(!1), s(null), d(t);
	}, p = async () => {
		l(!0);
		let e = await a();
		l(!1), s(null), d(e);
	};
	return /* @__PURE__ */ E("div", {
		class: "controls",
		children: [
			/* @__PURE__ */ E("div", {
				class: "controls__row",
				children: [/* @__PURE__ */ E("div", {
					class: "controls__label",
					children: "Governed by"
				}), /* @__PURE__ */ E("div", {
					class: "chips",
					children: n.length === 0 ? /* @__PURE__ */ E("span", {
						class: "faint",
						children: "No policies defined yet."
					}) : n.map((e) => /* @__PURE__ */ E("button", {
						type: "button",
						disabled: c || t.policy === e.name,
						class: "chip" + (t.policy === e.name ? " chip--on" : ""),
						onClick: () => {
							d(null), s({
								type: "bind",
								name: e.name
							});
						},
						children: [e.name, t.policy === e.name && /* @__PURE__ */ E("span", {
							class: "chip__now",
							children: " · current"
						})]
					}, e.name))
				})]
			}),
			/* @__PURE__ */ E("div", {
				class: "controls__row",
				children: [/* @__PURE__ */ E("div", {
					class: "controls__label",
					children: "Credentials"
				}), /* @__PURE__ */ E("button", {
					type: "button",
					class: "btn btn--danger btn--sm",
					disabled: c,
					onClick: () => {
						d(null), s({ type: "revoke" });
					},
					children: "Revoke credentials"
				})]
			}),
			o && /* @__PURE__ */ E("div", {
				class: "controls__confirm",
				children: [/* @__PURE__ */ E("span", {
					class: "controls__confirmtext",
					children: o.type === "bind" ? /* @__PURE__ */ E(e, { children: [
						"Bind policy ",
						/* @__PURE__ */ E("strong", { children: o.name }),
						" to ",
						/* @__PURE__ */ E("strong", { children: t.name }),
						"? The gateway enforces it on the pod's next request."
					] }) : /* @__PURE__ */ E(e, { children: [
						"Revoke every credential issued to ",
						/* @__PURE__ */ E("strong", { children: t.name }),
						"? Its brokered secrets stop working immediately."
					] })
				}), /* @__PURE__ */ E("div", {
					class: "controls__confirmbtns",
					children: [/* @__PURE__ */ E("button", {
						type: "button",
						disabled: c,
						class: "btn btn--sm " + (o.type === "revoke" ? "btn--danger" : "btn--primary"),
						onClick: () => o.type === "bind" ? f(o.name) : p(),
						children: c ? "Working…" : o.type === "bind" ? "Bind" : "Revoke"
					}), /* @__PURE__ */ E("button", {
						type: "button",
						class: "btn btn--ghost btn--sm",
						disabled: c,
						onClick: () => s(null),
						children: "Cancel"
					})]
				})]
			}),
			u && /* @__PURE__ */ E("div", {
				class: "controls__status " + (u.ok ? "ok" : "bad"),
				role: "status",
				children: u.msg
			})
		]
	});
}
//#endregion
export { Z as AttentionPanel, q as AuditLogTable, b as DECISIONS, M as DecisionBadge, ee as DestinationsTable, I as EgressChart, U as Fact, R as FleetLoad, a as HTTP_METHODS, O as ICONS, k as Icon, N as IntegrityBadge, P as IntegrityPanel, H as LiveDot, z as MixBar, X as OverviewCards, re as PodControls, Y as PodDetailPanel, J as PodFleetTable, F as PoddleMark, ne as PolicyEditor, $ as PolicyList, L as PostureBar, s as RANGE_MS, Q as RedactionsTable, j as SegmentedControl, B as SkelCards, V as SkelTable, D as Sparkline, A as StatCard, o as TIME_RANGES, w as bucketEvents, g as cap1, u as decide, x as decisionCounts, S as destinations, G as downloadCsv, d as dryRun, h as group, _ as humanKind, c as matchHost, l as methodsFor, v as relTime, C as rowKey, p as secretsFrom, m as summarise, y as threshTone, W as toCsv, f as toRows };
