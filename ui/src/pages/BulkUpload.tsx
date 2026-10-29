// Bulk Wire Upload: drag-and-drop or pick a CSV, download the template or a sample file. Upload validates
// synchronously (no Temporal involved yet) and opens the batch review page.
import { useRef, useState } from "react";
import { useNavigate } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Download, FileUp } from "lucide-react";
import { api, ApiError } from "../api";
import { session } from "../session";
import type { ValidationError } from "../types";
import { Skeleton } from "../components/ui";

const TEMPLATE = "originating_account,process_date,amount,currency,beneficiary_name,beneficiary_account,beneficiary_routing,beneficiary_bank,reference,subsidiary\n";

export function BulkUpload() {
  const navigate = useNavigate();
  const input = useRef<HTMLInputElement>(null);
  const [over, setOver] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<{ message: string; errors?: ValidationError[] } | null>(null);
  const samples = useQuery({ queryKey: ["samples"], queryFn: api.samples });

  const upload = async (file: File | undefined) => {
    if (!file) return;
    setBusy(true);
    setError(null);
    try {
      const summary = await api.upload(file);
      navigate(`/payments/bulk-wire/${summary.batchId}`);
    } catch (e) {
      const err = e as ApiError;
      if (err.status === 503) setError({ message: "The bank system is temporarily unavailable. Try again in a moment." });
      else if (err.status === 413) setError({ message: "The file is larger than 20 MB." });
      else setError({ message: err.message, errors: err.details as ValidationError[] | undefined });
    } finally {
      setBusy(false);
    }
  };

  const download = async (url: string, name: string) => {
    const res = await fetch(url, { headers: { Authorization: `Bearer ${session.token()}` } });
    const a = document.createElement("a");
    a.href = URL.createObjectURL(await res.blob());
    a.download = name;
    a.click();
  };

  return (
    <div className="stack">
      <div className="page-title"><h1>Bulk Wire Upload</h1></div>
      <section className="card">
        <div
          className={`dropzone ${over ? "over" : ""}`}
          role="button"
          tabIndex={0}
          onClick={() => input.current?.click()}
          onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && input.current?.click()}
          onDragOver={(e) => { e.preventDefault(); setOver(true); }}
          onDragLeave={() => setOver(false)}
          onDrop={(e) => { e.preventDefault(); setOver(false); upload(e.dataTransfer.files[0]); }}
        >
          <FileUp size={36} aria-hidden="true" />
          <p><strong>{busy ? "Uploading and validating…" : "Drop a CSV file here, or choose a file"}</strong></p>
          <p className="secondary">Up to 10,000 wires. Every row is validated before anything is sent to the bank.</p>
          <input ref={input} type="file" accept=".csv,text/csv" hidden onChange={(e) => upload(e.target.files?.[0])} />
        </div>
        {error && (
          <div className="banner danger" role="alert" style={{ marginTop: 14 }}>
            <div>
              {error.message}
              {error.errors && error.errors.length > 0 && <ul>{error.errors.map((x, i) => <li key={i}>{x.message}</li>)}</ul>}
            </div>
          </div>
        )}
        <div className="row" style={{ marginTop: 14 }}>
          <button className="btn" onClick={() => {
            const a = document.createElement("a");
            a.href = URL.createObjectURL(new Blob([TEMPLATE], { type: "text/csv" }));
            a.download = "bulk-wire-template.csv";
            a.click();
          }}><Download size={16} /> Download template</button>
        </div>
      </section>
      <section className="card">
        <h2>Sample files</h2>
        {samples.isPending ? <Skeleton /> : (
          <table>
            <thead><tr><th>File</th><th>Description</th><th className="num">Wires</th><th /></tr></thead>
            <tbody>
              {(samples.data ?? []).map((s) => (
                <tr key={s.name}>
                  <td className="mono">{s.name}.csv</td>
                  <td>{s.description}</td>
                  <td className="num">{s.rows.toLocaleString("en-US")}</td>
                  <td><button className="btn" onClick={() => download(api.sampleUrl(s.name), `${s.name}.csv`)}><Download size={15} /> Download</button></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </div>
  );
}
