import { useEffect, useState } from "react";
import { Copy, Plus, RefreshCw, X } from "lucide-react";

type Api = (path: string, options?: RequestInit) => Promise<Response>;
interface Capacity {
  active?: number; available?: number; effectiveLimit?: number; operatorLimit?: number;
  activeUnits?: number; availableUnits?: number; resourceReason?: string; reason?: string;
}
interface MachineMetadata { model?: string; user?: string; hostname?: string; os?: string; architecture?: string; cpuCores?: number; memoryBytes?: number }
interface Worker {
  nodeId: string; name?: string; version?: string; state?: string; connected: boolean;
  metadata?: MachineMetadata; paused?: boolean; revoked?: boolean; lastSeen?: string; capacity?: Capacity; capabilities?: string[];
}
interface Route { nodeId: string; documentId: string; attemptId: string; kind: string; status: string; createdAt: string }
const memory = (bytes?: number) => typeof bytes === "number" && Number.isFinite(bytes) && bytes > 0 ? `${(bytes / (1024 ** 3)).toFixed(1)} GB` : "Unknown";
const detail = (value?: string) => value?.trim() || "Unknown";
const date = (value?: string) => value && Number.isFinite(new Date(value).getTime()) ? new Date(value).toLocaleString() : "—";

async function read(response: Response) {
  if (!response.ok) throw new Error(`Request failed (${response.status}). Please try again.`);
  return response.json();
}

export function WorkersPage({ api }: { api: Api }) {
  const [nodes, setNodes] = useState<Worker[]>([]);
  const [authMode, setAuthMode] = useState<"shared" | "enrollment">("shared");
  const [routes, setRoutes] = useState<Route[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [refresh, setRefresh] = useState(0);
  const [pending, setPending] = useState("");
  const [code, setCode] = useState<{ token: string; expiresAt: string } | null>(null);
  const [copied, setCopied] = useState(false);
  const [updated, setUpdated] = useState<string>();

  useEffect(() => {
    let disposed = false;
    let timer: ReturnType<typeof setTimeout>;
    const controller = new AbortController();
    async function poll() {
      try {
        const data = await read(await api("/v1/admin/workers", { signal: controller.signal }));
        if (!disposed) {
          setAuthMode(data.authMode === "enrollment" ? "enrollment" : "shared");
          setNodes(data.nodes || []); setRoutes((data.routes || []).slice(0, 100));
          setError(""); setUpdated(new Date().toISOString());
        }
      } catch (e) {
        if (!disposed) setError(e instanceof Error ? e.message : "Could not load workers.");
      } finally {
        if (!disposed) { setLoading(false); timer = setTimeout(poll, 5000); }
      }
    }
    poll();
    return () => { disposed = true; controller.abort(); clearTimeout(timer); };
  }, [api, refresh]);

  async function enroll() {
    setPending("enrollment"); setError("");
    try {
      const result = await read(await api("/v1/admin/workers/enrollment", { method: "POST" }));
      setCode({ token: result.token, expiresAt: result.expiresAt }); setCopied(false);
    } catch (e) { setError(e instanceof Error ? e.message : "Could not create enrollment code."); }
    finally { setPending(""); }
  }

  async function control(nodeId: string, action: string) {
    if (action === "revoke" && !window.confirm(authMode === "shared" ? "Block this Mac’s node identity from receiving work? This does not rotate the shared app key or block other node identities." : "Revoke this Mac’s access? It must enroll again before receiving work.")) return;
    setPending(nodeId); setError("");
    try {
      const response = await api(`/v1/admin/workers/${encodeURIComponent(nodeId)}/control`, { method: "POST", body: JSON.stringify({ action }) });
      if (!response.ok) throw new Error(`Could not ${action} worker (${response.status}).`);
      setRefresh(value => value + 1);
    } catch (e) { setError(e instanceof Error ? e.message : "Could not update worker."); }
    finally { setPending(""); }
  }

  return <>
    <div className="page-header"><div><h1>Mac Workers & Routing</h1><p>Connect Macs from any network and follow how work is assigned.</p></div>
      <div className="worker-actions"><button className="btn btn-ghost" onClick={() => setRefresh(v => v + 1)}><RefreshCw size={16} />Refresh</button>
        {authMode === "enrollment" && <button className="btn btn-primary" disabled={!!pending || loading} onClick={enroll}><Plus size={16} />Add Mac</button>}</div></div>
    <div className="panel worker-guide"><strong>Install and connect a Mac</strong><p>{authMode === "enrollment" ? "Share the MacOCR app manually, open it on each Mac, enter your production server URL, then paste a fresh enrollment code from Add Mac. Each Mac needs its own code." : "Share the production MacOCR app manually and open it on each Mac. The app connects automatically using its bundled server configuration and shared key, with a separate node identity for each Mac."} Keep the app running with Internet access.</p>
      <small>Pause stops new assignments. Drain stops new assignments and lets current work finish; select Resume when the Mac is ready again. {authMode === "shared" ? "Revoke blocks this node identity; it does not rotate the shared key." : "Revoke removes the Mac’s access."}</small></div>
    {error && <div className="worker-error" role="alert">{error} Existing results may be out of date.</div>}
    <div className="panel"><div className="panel-header"><strong>{nodes.filter(n => n.connected && !n.revoked).length} connected · {nodes.length} registered</strong><span>Updates every 5 seconds · Last update {date(updated)}</span></div>
      <div className="table-responsive"><table className="data-table"><thead><tr><th>Mac / machine details</th><th>Connection / state</th><th>Capacity</th><th>Capabilities / version</th><th>Last seen</th><th>Controls</th></tr></thead>
        <tbody>{loading ? <tr><td colSpan={6}>Loading workers…</td></tr> : nodes.length === 0 ? <tr><td colSpan={6}>{authMode === "enrollment" ? "No Macs registered yet. Select Add Mac to connect your first worker." : "No Macs registered yet. Open the production MacOCR app on a Mac to connect it automatically."}</td></tr> : nodes.map(n => <tr key={n.nodeId}>
          <td><strong>{n.name || "Unnamed Mac"}</strong><div className="worker-id">{n.nodeId}</div><details className="worker-machine"><summary>Machine details</summary><dl>
            <dt>Model</dt><dd>{detail(n.metadata?.model)}</dd><dt>macOS</dt><dd>{detail(n.metadata?.os)}</dd>
            <dt>Architecture</dt><dd>{detail(n.metadata?.architecture)}</dd><dt>Local user</dt><dd>{detail(n.metadata?.user)}</dd>
            <dt>Hostname</dt><dd>{detail(n.metadata?.hostname)}</dd><dt>CPU cores</dt><dd>{typeof n.metadata?.cpuCores === "number" && n.metadata.cpuCores > 0 ? n.metadata.cpuCores : "Unknown"}</dd>
            <dt>RAM</dt><dd>{memory(n.metadata?.memoryBytes)}</dd>
          </dl></details></td>
          <td><span className={`badge ${n.connected && !n.revoked ? "badge-active" : "badge-disabled"}`}>{n.revoked ? "Revoked" : n.connected ? "Online" : "Offline"}</span><div>{n.paused ? "Paused" : n.state || "Unknown"}</div></td>
          <td>{n.capacity ? <><div>{n.capacity.active ?? 0} active · {n.capacity.available ?? 0} available</div><small>Limit {n.capacity.effectiveLimit ?? 0} / {n.capacity.operatorLimit ?? 0} · {n.capacity.availableUnits ?? 0} free units</small><div className="worker-id">{n.capacity.resourceReason || n.capacity.reason}</div></> : "—"}</td>
          <td>{n.capabilities?.join(", ") || "—"}<div className="worker-id">{n.version || "Version unavailable"}</div></td><td>{date(n.lastSeen)}</td>
          <td><div className="worker-actions">{!n.revoked && <><button className="btn btn-ghost btn-sm" disabled={!!pending} onClick={() => control(n.nodeId, n.paused ? "resume" : "pause")}>{n.paused ? "Resume" : "Pause"}</button><button className="btn btn-ghost btn-sm" disabled={!!pending} onClick={() => control(n.nodeId, "drain")}>Drain</button><button className="btn btn-ghost btn-sm" disabled={!!pending} onClick={() => control(n.nodeId, "revoke")}>Revoke</button></>}</div></td>
        </tr>)}</tbody></table></div></div>
    <div className="panel worker-routes"><div className="panel-header"><strong>Recent routing</strong><span>Latest {routes.length} assignments · operational history</span></div>
      <div className="table-responsive"><table className="data-table"><thead><tr><th>Assigned</th><th>Mac</th><th>Document / attempt</th><th>Kind</th><th>Status</th></tr></thead><tbody>
        {routes.length === 0 ? <tr><td colSpan={5}>No recent assignments.</td></tr> : routes.map((r, i) => <tr key={`${r.attemptId}-${i}`}><td>{date(r.createdAt)}</td><td>{nodes.find(n => n.nodeId === r.nodeId)?.name || r.nodeId}</td><td><div className="worker-id">{r.documentId}</div><small>{r.attemptId}</small></td><td>{r.kind}</td><td>{r.status}</td></tr>)}
      </tbody></table></div></div>
    {code && <div className="modal-overlay"><div className="modal-box" role="dialog" aria-modal="true" aria-labelledby="enrollment-title"><div className="worker-actions" style={{ justifyContent: "space-between" }}><h2 id="enrollment-title">Connect one Mac</h2><button className="btn btn-ghost btn-sm" aria-label="Close enrollment code" onClick={() => setCode(null)}><X size={18} /></button></div>
      <p>Paste this single-use code into the MacOCR app. It expires {date(code.expiresAt)}. Create another code for each additional Mac.</p><code className="worker-code">{code.token}</code>
      <div className="worker-actions"><button className="btn btn-primary" onClick={async () => { try { await navigator.clipboard.writeText(code.token); setCopied(true); } catch { setError("Clipboard unavailable. Select and copy the code manually."); } }}><Copy size={16} />{copied ? "Copied" : "Copy code"}</button><button className="btn btn-ghost" onClick={() => setCode(null)}>Done</button></div>
      <p>Share this code only with the person connecting the Mac. Closing this dialog clears it from this page.</p></div></div>}
  </>;
}
