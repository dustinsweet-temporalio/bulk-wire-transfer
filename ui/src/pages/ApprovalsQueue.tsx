// Approvals queue: batches drafted into Core and waiting for approval (Visibility: BulkWireStatus = DRAFTED).
import { useQuery } from "@tanstack/react-query";
import { api } from "../api";
import { Annotation, DOCS, Empty, Skeleton } from "../components/ui";
import { BatchTable } from "./Home";

export function ApprovalsQueue() {
  const list = useQuery({ queryKey: ["batches", "approvals"], queryFn: () => api.batches("approvals"), refetchInterval: 5000 });
  return (
    <div className="stack">
      <div className="page-title">
        <h1>Approvals</h1>
        <Annotation href={DOCS.searchAttributes}>Search Attribute: BulkWireStatus = DRAFTED</Annotation>
      </div>
      <section className="card">
        {list.isPending ? <Skeleton /> : list.isError ? <Empty>The approvals queue is temporarily unavailable.</Empty>
          : list.data!.length === 0 ? <Empty>No batches are waiting for approval.</Empty> : <BatchTable items={list.data!} />}
      </section>
    </div>
  );
}
