export type Event = {
    seq: number;
    time: string;
    source?: string;
    pod?: string;
    kind: string;
    upstream?: string;
    method?: string;
    path?: string;
    status?: number;
    decision?: string;
    detail?: string;
};
export type Policy = {
    name: string;
    allow_upstreams?: string[];
    deny_upstreams?: string[];
    methods?: Record<string, string[]>;
    egress?: string;
};
export type Pod = {
    name: string;
    state: string;
    size: string;
    mode: string;
    policy: string;
    autoscale: boolean;
    cpu: string;
    memPerc: string;
    mem: string;
};
export type Hist = Record<string, {
    cpu: number[];
    mem: number[];
}>;
export type Verify = {
    ok: boolean;
    brokenAt: number;
} | null;
export type Grouped = {
    pod: string;
    decision: string;
    upstream: string;
    count: number;
    secrets: number;
};
export type SegOption = {
    value: string;
    label: string;
    tone?: string;
};
export type Stats = {
    pods: number;
    requests: number;
    redactions: number;
    secrets: number;
    blocked: number;
    denied: number;
};
export type Dest = {
    upstream: string;
    total: number;
    allow: number;
    redact: number;
    deny: number;
    block: number;
    secrets: number;
    pods: Set<string>;
};
export type AllowRow = {
    host: string;
    methods: string[];
    open: boolean;
};
export type DryRow = {
    upstream: string;
    method: string;
    reason: string;
    count: number;
};
export type Cmd = {
    id: string;
    label: string;
    hint: string;
    icon: string;
    run: () => void;
};
export type Toast = {
    id: number;
    pod: string;
    decision: string;
    upstream: string;
};
export type Pending = {
    type: "bind";
    name: string;
} | {
    type: "revoke";
} | null;
export declare const HTTP_METHODS: string[];
