import { Fragment as e, options as t } from "preact";
import { useMemo as n, useState as r } from "preact/hooks";
//#region views/types.ts
var i = [
	"GET",
	"POST",
	"PUT",
	"PATCH",
	"DELETE",
	"HEAD",
	"OPTIONS"
];
//#endregion
//#region views/policy-eval.ts
function a(e, t) {
	for (let n of t) if (n === e || n.startsWith(".") && (e.endsWith(n) || e === n.slice(1))) return !0;
	return !1;
}
function o(e, t) {
	if (!e) return null;
	if (t in e) return e[t];
	for (let n in e) if (n.startsWith(".") && (t.endsWith(n) || t === n.slice(1))) return e[n];
	return null;
}
function s(e, t, n) {
	if (a(t, e.deny_upstreams || [])) return {
		allow: !1,
		reason: "on the deny-list"
	};
	if ((e.allow_upstreams || []).length > 0 && !a(t, e.allow_upstreams || [])) return {
		allow: !1,
		reason: "not allow-listed"
	};
	let r = o(e.methods, t);
	return r && n && n !== "CONNECT" && !r.some((e) => e.toUpperCase() === n.toUpperCase()) ? {
		allow: !1,
		reason: n + " not allowed here"
	} : {
		allow: !0,
		reason: ""
	};
}
function c(e, t) {
	let n = t.filter((e) => e.kind === "request" && e.upstream), r = /* @__PURE__ */ new Map(), i = 0;
	for (let t of n) {
		let n = s(e, t.upstream, t.method || "");
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
function l(e) {
	let t = e.methods || {};
	return [.../* @__PURE__ */ new Set([...e.allow_upstreams || [], ...Object.keys(t)])].map((e) => ({
		host: e,
		methods: t[e] || [],
		open: !1
	}));
}
//#endregion
//#region views/aggregate.ts
var u = (e) => {
	let t = (e || "").match(/redacted (\d+)/);
	return t ? +t[1] : 1;
};
function d(e) {
	let t = /* @__PURE__ */ new Set(), n = 0, r = 0, i = 0, a = 0, o = 0;
	for (let s of e) s.pod && t.add(s.pod), s.kind === "request" && n++, s.decision === "redact" && (r++, i += u(s.detail)), s.decision === "block" && a++, s.decision === "deny" && o++;
	return {
		pods: t.size,
		requests: n,
		redactions: r,
		secrets: i,
		blocked: a,
		denied: o
	};
}
function f(e, t) {
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
		i.count++, r.decision === "redact" && (i.secrets += u(r.detail)), n.set(e, i);
	}
	return [...n.values()].sort((e, t) => t.count - e.count);
}
var p = (e) => e && e.charAt(0).toUpperCase() + e.slice(1), m = (e) => p((e || "").replace(/\./g, " "));
function h(e) {
	let t = Math.max(0, Math.floor((Date.now() - new Date(e).getTime()) / 1e3));
	if (t < 5) return "just now";
	if (t < 60) return t + "s ago";
	let n = Math.floor(t / 60);
	if (n < 60) return n + "m ago";
	let r = Math.floor(n / 60);
	return r < 24 ? r + "h ago" : Math.floor(r / 24) + "d ago";
}
var g = (e) => e >= 85 ? "hot" : e >= 60 ? "warm" : "cool", _ = [
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
function v(e) {
	let t = {
		allow: 0,
		redact: 0,
		deny: 0,
		block: 0
	};
	for (let n of e) n.decision && n.decision in t && t[n.decision]++;
	return t;
}
//#endregion
//#region views/chart.ts
function y(e, t = 24) {
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
var b = 0;
Array.isArray;
function x(e, n, r, i, a, o) {
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
		__v: --b,
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
function S({ data: e }) {
	let t = 2.5;
	if (e.length < 2) return /* @__PURE__ */ x("span", {
		class: "spark spark--empty faint",
		children: "╌"
	});
	let n = e.length - 1, r = (e) => Math.min(Math.max(e, 0), 100), i = (e) => t + e / n * (80 - t * 2), a = (e) => 17.5 - r(e) / 100 * (20 - t * 2), o = e.map((e, t) => `${i(t).toFixed(1)},${a(e).toFixed(1)}`).join(" "), s = e[n];
	return /* @__PURE__ */ x("svg", {
		class: "spark spark--" + g(s),
		width: 80,
		height: 20,
		viewBox: "0 0 80 20",
		preserveAspectRatio: "none",
		"aria-hidden": "true",
		children: [
			/* @__PURE__ */ x("polygon", {
				class: "spark__area",
				points: `${i(0).toFixed(1)},17.5 ${o} ${i(n).toFixed(1)},17.5`
			}),
			/* @__PURE__ */ x("polyline", {
				class: "spark__line",
				points: o,
				fill: "none"
			}),
			/* @__PURE__ */ x("circle", {
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
function C({ n: e, label: t, tone: n }) {
	return /* @__PURE__ */ x("div", {
		class: "card" + (n ? " card--" + n : ""),
		children: [/* @__PURE__ */ x("div", {
			class: "card__num",
			children: e
		}), /* @__PURE__ */ x("div", {
			class: "card__label",
			children: t
		})]
	});
}
//#endregion
//#region views/SegmentedControl.tsx
function w({ value: e, options: t, onChange: n, ariaLabel: r }) {
	let i = Math.max(0, t.findIndex((t) => t.value === e));
	return /* @__PURE__ */ x("div", {
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
		children: t.map((t, r) => /* @__PURE__ */ x("button", {
			type: "button",
			role: "radio",
			"aria-checked": e === t.value,
			"data-tone": t.tone,
			tabIndex: r === i ? 0 : -1,
			class: "seg__opt" + (e === t.value ? " on" : ""),
			onClick: () => n(t.value),
			children: [t.label, t.badge != null && /* @__PURE__ */ x("span", {
				class: "seg__badge",
				"aria-hidden": "true",
				children: t.badge
			})]
		}))
	});
}
//#endregion
//#region views/DecisionBadge.tsx
function T({ decision: e }) {
	return /* @__PURE__ */ x("span", {
		class: "decision d-" + (e || ""),
		children: e || /* @__PURE__ */ x("span", {
			class: "faint",
			children: "—"
		})
	});
}
//#endregion
//#region views/Icon.tsx
var E = {
	overview: () => /* @__PURE__ */ x(e, { children: [
		/* @__PURE__ */ x("rect", {
			x: "3",
			y: "3",
			width: "7",
			height: "7",
			rx: "1.4"
		}),
		/* @__PURE__ */ x("rect", {
			x: "14",
			y: "3",
			width: "7",
			height: "7",
			rx: "1.4"
		}),
		/* @__PURE__ */ x("rect", {
			x: "14",
			y: "14",
			width: "7",
			height: "7",
			rx: "1.4"
		}),
		/* @__PURE__ */ x("rect", {
			x: "3",
			y: "14",
			width: "7",
			height: "7",
			rx: "1.4"
		})
	] }),
	pods: () => /* @__PURE__ */ x(e, { children: [
		/* @__PURE__ */ x("path", { d: "M21 8v8a2 2 0 0 1-1 1.73l-7 4a2 2 0 0 1-2 0l-7-4A2 2 0 0 1 3 16V8a2 2 0 0 1 1-1.73l7-4a2 2 0 0 1 2 0l7 4A2 2 0 0 1 21 8Z" }),
		/* @__PURE__ */ x("path", { d: "m3.3 7 8.7 5 8.7-5" }),
		/* @__PURE__ */ x("path", { d: "M12 22V12" })
	] }),
	audit: () => /* @__PURE__ */ x("path", { d: "M22 12h-4l-3 9L9 3l-3 9H2" }),
	policies: () => /* @__PURE__ */ x(e, { children: [/* @__PURE__ */ x("path", { d: "M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67 0C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1Z" }), /* @__PURE__ */ x("path", { d: "m9 12 2 2 4-4" })] }),
	globe: () => /* @__PURE__ */ x(e, { children: [/* @__PURE__ */ x("circle", {
		cx: "12",
		cy: "12",
		r: "10"
	}), /* @__PURE__ */ x("path", { d: "M12 2a15 15 0 0 1 0 20 15 15 0 0 1 0-20M2 12h20" })] }),
	eyeoff: () => /* @__PURE__ */ x(e, { children: [
		/* @__PURE__ */ x("path", { d: "M9.88 9.88a3 3 0 1 0 4.24 4.24" }),
		/* @__PURE__ */ x("path", { d: "M10.73 5.08A11 11 0 0 1 12 5c7 0 10 7 10 7a13 13 0 0 1-1.67 2.68" }),
		/* @__PURE__ */ x("path", { d: "M6.61 6.61A13 13 0 0 0 2 12s3 7 10 7a11 11 0 0 0 5.39-1.39" }),
		/* @__PURE__ */ x("line", {
			x1: "2",
			y1: "2",
			x2: "22",
			y2: "22"
		})
	] }),
	ban: () => /* @__PURE__ */ x(e, { children: [/* @__PURE__ */ x("circle", {
		cx: "12",
		cy: "12",
		r: "10"
	}), /* @__PURE__ */ x("path", { d: "m4.9 4.9 14.2 14.2" })] }),
	check: () => /* @__PURE__ */ x("path", { d: "M20 6 9 17l-5-5" }),
	octagon: () => /* @__PURE__ */ x(e, { children: [
		/* @__PURE__ */ x("polygon", { points: "7.86 2 16.14 2 22 7.86 22 16.14 16.14 22 7.86 22 2 16.14 2 7.86" }),
		/* @__PURE__ */ x("line", {
			x1: "15",
			y1: "9",
			x2: "9",
			y2: "15"
		}),
		/* @__PURE__ */ x("line", {
			x1: "9",
			y1: "9",
			x2: "15",
			y2: "15"
		})
	] }),
	panel: () => /* @__PURE__ */ x(e, { children: [/* @__PURE__ */ x("rect", {
		x: "3",
		y: "3",
		width: "18",
		height: "18",
		rx: "2"
	}), /* @__PURE__ */ x("line", {
		x1: "9",
		y1: "3",
		x2: "9",
		y2: "21"
	})] }),
	search: () => /* @__PURE__ */ x(e, { children: [/* @__PURE__ */ x("circle", {
		cx: "11",
		cy: "11",
		r: "7"
	}), /* @__PURE__ */ x("line", {
		x1: "21",
		y1: "21",
		x2: "16.65",
		y2: "16.65"
	})] }),
	theme: () => /* @__PURE__ */ x("path", { d: "M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z" })
};
function D({ name: e, size: t = 16 }) {
	let n = E[e];
	return n ? /* @__PURE__ */ x("svg", {
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
//#region views/IntegrityBadge.tsx
function O({ v: e, compact: t, href: n, onClick: r }) {
	let i = e == null ? "badge" : e.ok ? "badge ok" : "badge bad", a = e == null ? "Verifying…" : e.ok ? "Chain intact ✓" : `Chain broken @${e.brokenAt} ✗`, o = e == null ? "Checking the audit hash-chain…" : e.ok ? "Every audit event is hash-linked to the one before it, so any edit or deletion is detectable. Intact means nothing was tampered with. Click to open the audit trail." : `The audit hash-chain is broken at event #${e.brokenAt}: a row was altered or removed. Click to open the audit trail.`;
	return t ? /* @__PURE__ */ x("a", {
		class: i + " badge--icon",
		href: n,
		title: a,
		"aria-label": a,
		onClick: r,
		children: /* @__PURE__ */ x(D, {
			name: e && !e.ok ? "octagon" : "policies",
			size: 15
		})
	}) : /* @__PURE__ */ x("a", {
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
function k({ verify: e, checkedAt: t, recheck: n, count: r }) {
	let i = e == null ? "verifying" : e.ok ? "intact" : "broken", a = i === "verifying" ? "Verifying chain…" : i === "intact" ? "Audit chain intact" : `Chain broken at #${e.brokenAt}`;
	return /* @__PURE__ */ x("div", {
		class: "integrity integrity--" + i,
		children: [
			/* @__PURE__ */ x("span", {
				class: "integrity__icon",
				"aria-hidden": "true",
				children: /* @__PURE__ */ x(D, {
					name: i === "broken" ? "octagon" : "policies",
					size: 22
				})
			}),
			/* @__PURE__ */ x("div", {
				class: "integrity__body",
				children: [/* @__PURE__ */ x("div", {
					class: "integrity__status",
					children: a
				}), /* @__PURE__ */ x("p", {
					class: "integrity__desc",
					children: i === "broken" ? "An event was altered or removed after it was written — everything from that point on is suspect." : "Every event is hash-linked to the one before it, so any edit or deletion is detectable after the fact."
				})]
			}),
			/* @__PURE__ */ x("dl", {
				class: "integrity__meta",
				children: [/* @__PURE__ */ x("div", { children: [/* @__PURE__ */ x("dt", { children: "Events" }), /* @__PURE__ */ x("dd", { children: r })] }), /* @__PURE__ */ x("div", { children: [/* @__PURE__ */ x("dt", { children: "Last verified" }), /* @__PURE__ */ x("dd", { children: t ? h(new Date(t).toISOString()) : "…" })] })]
			}),
			/* @__PURE__ */ x("button", {
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
function A({ size: e = 30 }) {
	return /* @__PURE__ */ x("svg", {
		class: "pmark",
		width: e,
		height: e,
		viewBox: "382.0 134.1 435.9 435.9",
		"aria-hidden": "true",
		children: [
			/* @__PURE__ */ x("path", {
				d: "M769.71,450.00 L769.71,254.04 L600.00,352.02 L600.00,547.98 Z",
				fill: "currentColor"
			}),
			/* @__PURE__ */ x("path", {
				d: "M600.00,547.98 L600.00,352.02 L430.29,254.04 L430.29,450.00 Z",
				fill: "currentColor"
			}),
			/* @__PURE__ */ x("path", {
				d: "M769.71,254.04 L600.00,156.06 L430.29,254.04 L600.00,352.02 Z",
				fill: "#2f9e6f"
			}),
			/* @__PURE__ */ x("g", {
				transform: "matrix(169.7056,97.9787,0.0000,195.9601,430.29,254.04)",
				fill: "#2f9e6f",
				children: [/* @__PURE__ */ x("path", { d: "M0.19,0.31 L0.29,0.31 L0.44,0.50 L0.29,0.69 L0.19,0.69 L0.34,0.50 Z" }), /* @__PURE__ */ x("path", { d: "M0.50,0.605 L0.72,0.605 L0.72,0.685 L0.50,0.685 Z" })]
			})
		]
	});
}
//#endregion
//#region views/EgressChart.tsx
function j({ events: e }) {
	let [t, i] = r(null), a = n(() => y(e, 14), [e]);
	if (a.length === 0) return /* @__PURE__ */ x("div", {
		class: "chart-empty",
		children: "No egress yet. Requests chart here as your agents run."
	});
	let o = a.length, s = Math.max(1, ...a.map((e) => e.req)), c = 984 / o, l = Math.min(46, c * .6), u = (e) => 8 + (e + .5) * c, d = (e) => e / s * 136, f = a.reduce((e, t) => e + t.req, 0), p = a.reduce((e, t) => e + t.intervened, 0), m = t == null ? null : a[t];
	return /* @__PURE__ */ x("div", {
		class: "chart",
		children: [/* @__PURE__ */ x("svg", {
			class: "plot",
			viewBox: "0 0 1000 172",
			preserveAspectRatio: "xMidYMid meet",
			role: "img",
			"aria-label": `Egress over time: ${f} requests, ${p} redacted or blocked, across ${o} intervals`,
			children: [
				/* @__PURE__ */ x("line", {
					class: "grid grid--soft",
					x1: 8,
					y1: 14,
					x2: 992,
					y2: 14,
					"vector-effect": "non-scaling-stroke"
				}),
				/* @__PURE__ */ x("text", {
					class: "axtick",
					x: 8,
					y: 10,
					children: s
				}),
				/* @__PURE__ */ x("line", {
					class: "grid",
					x1: 8,
					y1: 150,
					x2: 992,
					y2: 150,
					"vector-effect": "non-scaling-stroke"
				}),
				a.map((e, n) => {
					let r = e.req - e.intervened, a = d(r), o = d(e.intervened), s = u(n) - l / 2, f = t != null && t !== n ? " bar--dim" : "", p = e.intervened > 0 && r > 0 ? 2 : 0;
					return /* @__PURE__ */ x("g", { children: [
						r > 0 && /* @__PURE__ */ x("rect", {
							class: "bar bar--allow" + f,
							x: s,
							y: 150 - a,
							width: l,
							height: a,
							rx: "3"
						}),
						e.intervened > 0 && /* @__PURE__ */ x("rect", {
							class: "bar bar--int" + f,
							x: s,
							y: 150 - a - p - o,
							width: l,
							height: o,
							rx: "3"
						}),
						/* @__PURE__ */ x("rect", {
							x: u(n) - c / 2,
							y: 14,
							width: c,
							height: 136,
							fill: "transparent",
							onMouseEnter: () => i(n),
							onMouseLeave: () => i(null)
						})
					] }, n);
				}),
				/* @__PURE__ */ x("text", {
					class: "axlabel",
					x: 8,
					y: 166,
					"text-anchor": "start",
					children: h(new Date(a[0].t0).toISOString())
				}),
				/* @__PURE__ */ x("text", {
					class: "axlabel",
					x: 992,
					y: 166,
					"text-anchor": "end",
					children: "now"
				})
			]
		}), m && /* @__PURE__ */ x("div", {
			class: "tip",
			style: `left:${((t + .5) / o * 100).toFixed(2)}%`,
			"aria-hidden": "true",
			children: [
				/* @__PURE__ */ x("div", {
					class: "tip__t",
					children: [
						h(new Date(m.t0).toISOString()),
						" · ",
						m.req,
						" total"
					]
				}),
				/* @__PURE__ */ x("div", {
					class: "tip__row",
					children: [/* @__PURE__ */ x("span", {
						class: "tip__k",
						children: [/* @__PURE__ */ x("span", { class: "dotmark dotmark--req" }), "Allowed"]
					}), /* @__PURE__ */ x("span", {
						class: "tip__v",
						children: m.req - m.intervened
					})]
				}),
				/* @__PURE__ */ x("div", {
					class: "tip__row",
					children: [/* @__PURE__ */ x("span", {
						class: "tip__k",
						children: [/* @__PURE__ */ x("span", { class: "dotmark dotmark--int" }), "Intervened"]
					}), /* @__PURE__ */ x("span", {
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
function M({ counts: e }) {
	let t = _.reduce((t, n) => t + (e[n.key] || 0), 0);
	if (t === 0) return /* @__PURE__ */ x("div", {
		class: "chart-empty",
		children: "No decisions recorded yet."
	});
	let n = (e) => Math.round(e / t * 100);
	return /* @__PURE__ */ x("div", {
		class: "posture",
		children: [/* @__PURE__ */ x("div", {
			class: "posture__bar",
			role: "img",
			"aria-label": "Decision mix: " + _.map((t) => `${e[t.key] || 0} ${t.label}`).join(", "),
			children: _.filter((t) => (e[t.key] || 0) > 0).map((t) => /* @__PURE__ */ x("div", {
				class: "posture__seg d-" + t.key,
				style: `flex-grow:${e[t.key]}`,
				title: `${t.label}: ${e[t.key]} (${n(e[t.key])}%)`
			}, t.key))
		}), /* @__PURE__ */ x("ul", {
			class: "legend",
			children: _.map((t) => /* @__PURE__ */ x("li", {
				class: "legend__i",
				children: [
					/* @__PURE__ */ x("span", {
						class: "legend__mk d-" + t.key,
						children: /* @__PURE__ */ x(D, {
							name: t.icon,
							size: 13
						})
					}),
					/* @__PURE__ */ x("span", {
						class: "legend__lb",
						children: t.label
					}),
					/* @__PURE__ */ x("span", {
						class: "legend__v",
						children: e[t.key] || 0
					}),
					/* @__PURE__ */ x("span", {
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
function N({ pods: e }) {
	let t = e.filter((e) => e.state === "running");
	return t.length === 0 ? /* @__PURE__ */ x("div", {
		class: "chart-empty",
		children: "No pods running right now."
	}) : /* @__PURE__ */ x("div", {
		class: "fleet",
		children: t.map((e) => {
			let t = parseFloat(e.cpu) || 0;
			return /* @__PURE__ */ x("div", {
				class: "fleet__row",
				title: `${e.name}: CPU ${e.cpu}, memory ${e.memPerc}`,
				children: [
					/* @__PURE__ */ x("span", {
						class: "fleet__name",
						children: e.name
					}),
					/* @__PURE__ */ x("span", {
						class: "fleet__track",
						"aria-hidden": "true",
						children: /* @__PURE__ */ x("span", {
							class: "fleet__fill fleet__fill--" + g(t),
							style: `width:${Math.min(100, t)}%`
						})
					}),
					/* @__PURE__ */ x("span", {
						class: "fleet__val c-mono",
						children: e.cpu || "—"
					}),
					/* @__PURE__ */ x("span", {
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
function P({ d: e }) {
	let t = [
		["allow", e.allow],
		["redact", e.redact],
		["deny", e.deny],
		["block", e.block]
	].filter(([, e]) => e > 0);
	return /* @__PURE__ */ x("span", {
		class: "mix",
		role: "img",
		"aria-label": t.map(([e, t]) => `${t} ${e}`).join(", "),
		children: t.map(([e, t]) => /* @__PURE__ */ x("span", {
			class: "mix__seg d-" + e,
			style: `flex-grow:${t}`,
			title: `${e}: ${t}`
		}, e))
	});
}
//#endregion
//#region views/Skeletons.tsx
function F() {
	return /* @__PURE__ */ x("div", {
		class: "cards",
		"aria-hidden": "true",
		children: [
			0,
			1,
			2,
			3
		].map((e) => /* @__PURE__ */ x("div", {
			class: "card",
			children: [/* @__PURE__ */ x("span", { class: "skel skel--num" }), /* @__PURE__ */ x("span", { class: "skel skel--sm" })]
		}, e))
	});
}
function I({ rows: e = 6 }) {
	return /* @__PURE__ */ x("div", {
		class: "table-wrap skel-table",
		"aria-hidden": "true",
		"aria-busy": "true",
		children: Array.from({ length: e }).map((e, t) => /* @__PURE__ */ x("div", {
			class: "skel-tr",
			children: /* @__PURE__ */ x("span", { class: "skel" })
		}, t))
	});
}
//#endregion
//#region views/LiveDot.tsx
function L({ status: e }) {
	let t = e === "live" ? "Live" : e === "down" ? "Reconnecting" : "Connecting";
	return /* @__PURE__ */ x("span", {
		class: "live live--" + e,
		title: "Audit stream: " + t,
		role: "status",
		children: [/* @__PURE__ */ x("span", {
			class: "live__dot",
			"aria-hidden": "true"
		}), t]
	});
}
//#endregion
//#region views/Fact.tsx
function R({ label: e, children: t }) {
	return /* @__PURE__ */ x("div", { children: [/* @__PURE__ */ x("dt", { children: e }), /* @__PURE__ */ x("dd", { children: t })] });
}
//#endregion
export { _ as DECISIONS, T as DecisionBadge, j as EgressChart, R as Fact, N as FleetLoad, i as HTTP_METHODS, E as ICONS, D as Icon, O as IntegrityBadge, k as IntegrityPanel, L as LiveDot, P as MixBar, A as PoddleMark, M as PostureBar, w as SegmentedControl, F as SkelCards, I as SkelTable, S as Sparkline, C as StatCard, y as bucketEvents, p as cap1, s as decide, v as decisionCounts, c as dryRun, f as group, m as humanKind, a as matchHost, o as methodsFor, h as relTime, u as secretsFrom, d as summarise, g as threshTone, l as toRows };
