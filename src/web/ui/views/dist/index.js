//#region views/types.ts
var e = [
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
function t(e, t) {
	for (let n of t) if (n === e || n.startsWith(".") && (e.endsWith(n) || e === n.slice(1))) return !0;
	return !1;
}
function n(e, t) {
	if (!e) return null;
	if (t in e) return e[t];
	for (let n in e) if (n.startsWith(".") && (t.endsWith(n) || t === n.slice(1))) return e[n];
	return null;
}
function r(e, r, i) {
	if (t(r, e.deny_upstreams || [])) return {
		decision: "deny",
		reason: "on the deny-list"
	};
	if ((e.allow_upstreams || []).length > 0 && !t(r, e.allow_upstreams || [])) return {
		decision: "deny",
		reason: "not allow-listed"
	};
	let a = n(e.methods, r);
	return a && i && i !== "CONNECT" && !a.some((e) => e.toUpperCase() === i.toUpperCase()) ? {
		decision: "block",
		reason: i + " not allowed here"
	} : {
		decision: "allow",
		reason: ""
	};
}
function i(e, t) {
	let n = t.filter((e) => e.kind === "request" && e.upstream), i = /* @__PURE__ */ new Map();
	for (let t of n) {
		let n = r(e, t.upstream, t.method || "");
		if (n.decision === "allow") continue;
		let a = `${t.method || ""}|${t.upstream}`, o = i.get(a) || {
			upstream: t.upstream,
			method: t.method || "",
			reason: n.reason,
			count: 0
		};
		o.count++, i.set(a, o);
	}
	return [...i.values()].sort((e, t) => t.count - e.count);
}
function a(e) {
	let t = e.methods || {};
	return [.../* @__PURE__ */ new Set([...e.allow_upstreams || [], ...Object.keys(t)])].map((e) => ({
		host: e,
		methods: t[e] || [],
		open: !1
	}));
}
//#endregion
export { e as HTTP_METHODS, r as decide, i as dryRun, t as matchHost, n as methodsFor, a as toRows };
