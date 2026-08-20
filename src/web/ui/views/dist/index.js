import { Fragment as e, options as t } from "preact";
import { useEffect as n, useMemo as r, useState as i } from "preact/hooks";
//#region views/aggregate.ts
var a = (e) => {
	let t = (e || "").match(/redacted (\d+)/);
	return t ? +t[1] : 1;
};
function o(e) {
	let t = /* @__PURE__ */ new Set(), n = 0, r = 0, i = 0, o = 0, s = 0;
	for (let c of e) c.pod && t.add(c.pod), c.kind === "request" && n++, c.decision === "redact" && (r++, i += a(c.detail)), c.decision === "block" && o++, c.decision === "deny" && s++;
	return {
		pods: t.size,
		requests: n,
		redactions: r,
		secrets: i,
		blocked: o,
		denied: s
	};
}
function s(e, t) {
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
		i.count++, r.decision === "redact" && (i.secrets += a(r.detail)), n.set(e, i);
	}
	return [...n.values()].sort((e, t) => t.count - e.count);
}
var c = (e) => e && e.charAt(0).toUpperCase() + e.slice(1), l = (e) => c((e || "").replace(/\./g, " "));
function u(e) {
	let t = Math.max(0, Math.floor((Date.now() - new Date(e).getTime()) / 1e3));
	if (t < 5) return "just now";
	if (t < 60) return t + "s ago";
	let n = Math.floor(t / 60);
	if (n < 60) return n + "m ago";
	let r = Math.floor(n / 60);
	return r < 24 ? r + "h ago" : Math.floor(r / 24) + "d ago";
}
var d = (e) => e >= 85 ? "hot" : e >= 60 ? "warm" : "cool", f = 0;
Array.isArray;
function p(e, n, r, i, a, o) {
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
		__v: --f,
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
function m({ data: e }) {
	let t = 2.5;
	if (e.length < 2) return /* @__PURE__ */ p("span", {
		class: "spark spark--empty faint",
		children: "╌"
	});
	let n = e.length - 1, r = (e) => Math.min(Math.max(e, 0), 100), i = (e) => t + e / n * (80 - t * 2), a = (e) => 17.5 - r(e) / 100 * (20 - t * 2), o = e.map((e, t) => `${i(t).toFixed(1)},${a(e).toFixed(1)}`).join(" "), s = e[n];
	return /* @__PURE__ */ p("svg", {
		class: "spark spark--" + d(s),
		width: 80,
		height: 20,
		viewBox: "0 0 80 20",
		preserveAspectRatio: "none",
		"aria-hidden": "true",
		children: [
			/* @__PURE__ */ p("polygon", {
				class: "spark__area",
				points: `${i(0).toFixed(1)},17.5 ${o} ${i(n).toFixed(1)},17.5`
			}),
			/* @__PURE__ */ p("polyline", {
				class: "spark__line",
				points: o,
				fill: "none"
			}),
			/* @__PURE__ */ p("circle", {
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
function h({ n: e, label: t, tone: n }) {
	return /* @__PURE__ */ p("div", {
		class: "card" + (n ? " card--" + n : ""),
		children: [/* @__PURE__ */ p("div", {
			class: "card__num",
			children: e
		}), /* @__PURE__ */ p("div", {
			class: "card__label",
			children: t
		})]
	});
}
//#endregion
//#region views/SegmentedControl.tsx
function g({ value: e, options: t, onChange: n, ariaLabel: r }) {
	let i = Math.max(0, t.findIndex((t) => t.value === e));
	return /* @__PURE__ */ p("div", {
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
		children: t.map((t, r) => /* @__PURE__ */ p("button", {
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
function _({ decision: e }) {
	return /* @__PURE__ */ p("span", {
		class: "decision d-" + (e || ""),
		children: e || /* @__PURE__ */ p("span", {
			class: "faint",
			children: "—"
		})
	});
}
//#endregion
//#region views/IntegrityBadge.tsx
function v({ v: e }) {
	return e ? e.ok ? /* @__PURE__ */ p("span", {
		class: "badge ok",
		children: "chain intact ✓"
	}) : /* @__PURE__ */ p("span", {
		class: "badge bad",
		children: [
			"chain broken @",
			e.brokenAt,
			" ✗"
		]
	}) : /* @__PURE__ */ p("span", {
		class: "badge",
		children: "verifying…"
	});
}
//#endregion
//#region views/AuditLogTable.tsx
var y = [
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
function b({ events: e, initialPod: t }) {
	let [a, o] = i(t || ""), [s, d] = i("");
	n(() => {
		t && o(t);
	}, [t]);
	let f = r(() => e.filter((e) => {
		if (s && e.decision !== s) return !1;
		if (!a) return !0;
		let t = a.toLowerCase();
		return (e.pod || "").toLowerCase().includes(t) || (e.kind || "").toLowerCase().includes(t) || (e.upstream || "").toLowerCase().includes(t);
	}), [
		e,
		a,
		s
	]);
	return /* @__PURE__ */ p("div", { children: [/* @__PURE__ */ p("div", {
		class: "toolbar",
		children: [
			/* @__PURE__ */ p("input", {
				class: "grow",
				"aria-label": "Filter events by pod, kind, or upstream",
				placeholder: "Filter by pod, kind, or upstream…",
				value: a,
				onInput: (e) => o(e.target.value)
			}),
			/* @__PURE__ */ p(g, {
				value: s,
				options: y,
				onChange: d,
				ariaLabel: "filter by decision"
			}),
			/* @__PURE__ */ p("span", {
				class: "count",
				children: [f.length, " events"]
			})
		]
	}), /* @__PURE__ */ p("div", {
		class: "table-wrap",
		children: /* @__PURE__ */ p("table", {
			class: "dense",
			children: [/* @__PURE__ */ p("thead", { children: /* @__PURE__ */ p("tr", { children: [
				/* @__PURE__ */ p("th", {
					scope: "col",
					children: "time"
				}),
				/* @__PURE__ */ p("th", {
					scope: "col",
					children: "pod"
				}),
				/* @__PURE__ */ p("th", {
					scope: "col",
					children: "kind"
				}),
				/* @__PURE__ */ p("th", {
					scope: "col",
					children: "decision"
				}),
				/* @__PURE__ */ p("th", {
					scope: "col",
					children: "upstream"
				}),
				/* @__PURE__ */ p("th", {
					scope: "col",
					children: "detail"
				})
			] }) }), /* @__PURE__ */ p("tbody", { children: [f.length === 0 && /* @__PURE__ */ p("tr", { children: /* @__PURE__ */ p("td", {
				colSpan: 6,
				class: "empty",
				children: a || s ? "No events match your filter." : "Monitoring active — no events recorded yet."
			}) }), f.slice(0, 800).map((e) => /* @__PURE__ */ p("tr", { children: [
				/* @__PURE__ */ p("td", {
					class: "c-time",
					title: new Date(e.time).toLocaleString(),
					children: u(e.time)
				}),
				/* @__PURE__ */ p("td", {
					class: "c-pod",
					children: e.pod || /* @__PURE__ */ p("span", {
						class: "faint",
						children: "—"
					})
				}),
				/* @__PURE__ */ p("td", { children: l(e.kind) }),
				/* @__PURE__ */ p("td", { children: /* @__PURE__ */ p(_, { decision: e.decision }) }),
				/* @__PURE__ */ p("td", {
					class: "c-mono",
					children: e.upstream || /* @__PURE__ */ p("span", {
						class: "faint",
						children: "—"
					})
				}),
				/* @__PURE__ */ p("td", {
					class: "c-detail",
					children: c(e.detail || "")
				})
			] }, e.seq))] })]
		})
	})] });
}
//#endregion
//#region views/Fact.tsx
function x({ label: e, children: t }) {
	return /* @__PURE__ */ p("div", { children: [/* @__PURE__ */ p("dt", { children: e }), /* @__PURE__ */ p("dd", { children: t })] });
}
//#endregion
//#region views/PodFleetTable.tsx
function S({ pods: e, hist: t, onPod: n, emptyState: r }) {
	return /* @__PURE__ */ p("div", {
		class: "table-wrap",
		children: /* @__PURE__ */ p("table", { children: [/* @__PURE__ */ p("thead", { children: /* @__PURE__ */ p("tr", { children: [
			/* @__PURE__ */ p("th", {
				scope: "col",
				children: "pod"
			}),
			/* @__PURE__ */ p("th", {
				scope: "col",
				children: "state"
			}),
			/* @__PURE__ */ p("th", {
				scope: "col",
				children: "size"
			}),
			/* @__PURE__ */ p("th", {
				scope: "col",
				children: "mode"
			}),
			/* @__PURE__ */ p("th", {
				scope: "col",
				children: "policy"
			}),
			/* @__PURE__ */ p("th", {
				scope: "col",
				class: "num",
				children: "cpu"
			}),
			/* @__PURE__ */ p("th", {
				scope: "col",
				class: "num",
				children: "memory"
			})
		] }) }), /* @__PURE__ */ p("tbody", { children: [e.length === 0 && /* @__PURE__ */ p("tr", { children: /* @__PURE__ */ p("td", {
			colSpan: 7,
			class: "empty",
			children: r
		}) }), e.map((e) => {
			let r = t[e.name] || {
				cpu: [],
				mem: []
			};
			return /* @__PURE__ */ p("tr", {
				class: "clickable",
				onClick: () => n(e.name),
				children: [
					/* @__PURE__ */ p("td", {
						class: "c-pod",
						children: [e.name, e.autoscale && /* @__PURE__ */ p("span", {
							class: "tag",
							children: "auto"
						})]
					}),
					/* @__PURE__ */ p("td", { children: /* @__PURE__ */ p("span", {
						class: "state state--" + e.state,
						children: e.state
					}) }),
					/* @__PURE__ */ p("td", {
						class: "c-mono",
						children: c(e.size)
					}),
					/* @__PURE__ */ p("td", {
						class: "c-mono",
						children: e.mode ? c(e.mode) : /* @__PURE__ */ p("span", {
							class: "faint",
							children: "—"
						})
					}),
					/* @__PURE__ */ p("td", {
						class: "c-mono",
						children: e.policy || /* @__PURE__ */ p("span", {
							class: "faint",
							children: "—"
						})
					}),
					/* @__PURE__ */ p("td", {
						class: "perf",
						children: [/* @__PURE__ */ p(m, { data: r.cpu }), /* @__PURE__ */ p("span", {
							class: "c-mono",
							children: e.cpu || "—"
						})]
					}),
					/* @__PURE__ */ p("td", {
						class: "perf",
						children: [/* @__PURE__ */ p(m, { data: r.mem }), /* @__PURE__ */ p("span", {
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
function C({ name: e, pod: t, hist: n, events: r, onBack: i, onPolicyClick: a }) {
	return /* @__PURE__ */ p("div", { children: [
		/* @__PURE__ */ p("div", {
			class: "detail-head",
			children: [
				/* @__PURE__ */ p("a", {
					href: "/pods",
					class: "back",
					onClick: i,
					children: "← Pods"
				}),
				/* @__PURE__ */ p("h1", {
					class: "detail-title",
					children: e
				}),
				t ? /* @__PURE__ */ p("span", {
					class: "state state--" + t.state,
					children: t.state
				}) : /* @__PURE__ */ p("span", {
					class: "state state--stopped",
					children: "not running"
				}),
				t?.autoscale && /* @__PURE__ */ p("span", {
					class: "tag",
					children: "auto"
				})
			]
		}),
		t && /* @__PURE__ */ p("dl", {
			class: "facts",
			children: [
				/* @__PURE__ */ p(x, {
					label: "size",
					children: /* @__PURE__ */ p("span", {
						class: "c-mono",
						children: c(t.size)
					})
				}),
				/* @__PURE__ */ p(x, {
					label: "mode",
					children: /* @__PURE__ */ p("span", {
						class: "c-mono",
						children: t.mode ? c(t.mode) : "—"
					})
				}),
				/* @__PURE__ */ p(x, {
					label: "policy",
					children: t.policy ? /* @__PURE__ */ p("a", {
						class: "fact-link c-mono",
						href: `/policies/${encodeURIComponent(t.policy)}`,
						onClick: a,
						children: t.policy
					}) : /* @__PURE__ */ p("span", {
						class: "faint",
						children: "none"
					})
				}),
				/* @__PURE__ */ p(x, {
					label: "cpu",
					children: /* @__PURE__ */ p("span", {
						class: "perf-inline",
						children: [/* @__PURE__ */ p(m, { data: n.cpu }), /* @__PURE__ */ p("span", {
							class: "c-mono",
							children: t.cpu || "—"
						})]
					})
				}),
				/* @__PURE__ */ p(x, {
					label: "memory",
					children: /* @__PURE__ */ p("span", {
						class: "perf-inline",
						children: [/* @__PURE__ */ p(m, { data: n.mem }), /* @__PURE__ */ p("span", {
							class: "c-mono",
							children: t.mem || "—"
						})]
					})
				})
			]
		}),
		/* @__PURE__ */ p("h2", {
			class: "section-title",
			children: "Audit trail"
		}),
		/* @__PURE__ */ p(b, {
			events: r,
			initialPod: e
		})
	] });
}
//#endregion
//#region views/OverviewCards.tsx
function w({ stats: e }) {
	return /* @__PURE__ */ p("div", {
		class: "cards",
		children: [
			/* @__PURE__ */ p(h, {
				n: e.pods,
				label: "pods active"
			}),
			/* @__PURE__ */ p(h, {
				n: e.requests,
				label: "requests"
			}),
			/* @__PURE__ */ p(h, {
				n: e.secrets,
				label: "secrets redacted",
				tone: e.secrets ? "warn" : void 0
			}),
			/* @__PURE__ */ p(h, {
				n: e.blocked + e.denied,
				label: "blocked / denied",
				tone: e.blocked + e.denied ? "flag" : void 0
			})
		]
	});
}
//#endregion
//#region views/AttentionPanel.tsx
function T({ attention: t, onPod: n }) {
	return /* @__PURE__ */ p(e, { children: [/* @__PURE__ */ p("h2", {
		class: "section-title",
		children: "Attention"
	}), t.length === 0 ? /* @__PURE__ */ p("div", {
		class: "panel empty",
		children: "No policy denials or blocks — agents are inside their guardrails."
	}) : /* @__PURE__ */ p("div", {
		class: "panel",
		children: t.map((e) => /* @__PURE__ */ p("button", {
			class: "attn",
			onClick: () => n(e.pod),
			children: [
				/* @__PURE__ */ p("span", {
					class: "attn__pod",
					children: e.pod
				}),
				/* @__PURE__ */ p("span", {
					class: "attn__desc",
					children: [
						/* @__PURE__ */ p(_, { decision: e.decision }),
						" ",
						e.upstream
					]
				}),
				/* @__PURE__ */ p("span", {
					class: "attn__count",
					children: ["×", e.count]
				})
			]
		}))
	})] });
}
//#endregion
//#region views/RedactionsTable.tsx
function E({ redactions: t, onPod: n }) {
	return /* @__PURE__ */ p(e, { children: [/* @__PURE__ */ p("h2", {
		class: "section-title",
		children: "Secrets redacted"
	}), t.length === 0 ? /* @__PURE__ */ p("div", {
		class: "panel empty",
		children: "No secrets redacted yet — redact-mode policies strip credentials the agent tries to send."
	}) : /* @__PURE__ */ p("div", {
		class: "table-wrap",
		children: /* @__PURE__ */ p("table", { children: [/* @__PURE__ */ p("thead", { children: /* @__PURE__ */ p("tr", { children: [
			/* @__PURE__ */ p("th", { children: "pod" }),
			/* @__PURE__ */ p("th", { children: "destination" }),
			/* @__PURE__ */ p("th", { children: "secrets" }),
			/* @__PURE__ */ p("th", { children: "times" })
		] }) }), /* @__PURE__ */ p("tbody", { children: t.map((e) => /* @__PURE__ */ p("tr", {
			onClick: () => n(e.pod),
			class: "clickable",
			children: [
				/* @__PURE__ */ p("td", {
					class: "c-pod",
					children: e.pod
				}),
				/* @__PURE__ */ p("td", {
					class: "c-mono",
					children: e.upstream
				}),
				/* @__PURE__ */ p("td", {
					class: "c-mono",
					children: e.secrets
				}),
				/* @__PURE__ */ p("td", {
					class: "c-mono",
					children: ["×", e.count]
				})
			]
		})) })] })
	})] });
}
//#endregion
//#region views/PolicyList.tsx
function D({ policies: e, selectedName: t, onSelect: n, onNew: r }) {
	return /* @__PURE__ */ p("div", {
		class: "list",
		children: [e.map((e) => /* @__PURE__ */ p("a", {
			href: `/policies/${encodeURIComponent(e.name)}`,
			onClick: (t) => {
				t.preventDefault(), n(e.name);
			},
			class: t === e.name ? "on" : "",
			children: e.name
		}, e.name)), /* @__PURE__ */ p("a", {
			href: "/policies/new",
			onClick: (e) => {
				e.preventDefault(), r();
			},
			class: "new",
			children: "＋ New policy"
		})]
	});
}
//#endregion
export { T as AttentionPanel, b as AuditLogTable, _ as DecisionBadge, x as Fact, v as IntegrityBadge, w as OverviewCards, C as PodDetailPanel, S as PodFleetTable, D as PolicyList, E as RedactionsTable, g as SegmentedControl, m as Sparkline, h as StatCard, c as cap1, s as group, l as humanKind, u as relTime, o as summarise, d as threshTone };
