// /payments/bulk-wire/:batchId — the staged review before a workflow exists, the live detail afterwards.
// PATTERN: Query for State. Polled every 1000 ms while the batch is not terminal; the live view comes only from
// the workflow's `status` Query (plus DescribeWorkflowExecution for retry telemetry).
import { useParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { api, ApiError } from "../api";
import { isTerminal } from "../format";
import { Empty, Skeleton } from "../components/ui";
import { StagedReview } from "./StagedReview";
import { LiveDetail } from "./LiveDetail";

export function BatchPage() {
  const { batchId = "" } = useParams();
  const status = useQuery({
    queryKey: ["batch", batchId],
    queryFn: () => api.batch(batchId),
    refetchInterval: (q) => {
      const d = q.state.data;
      if (!d) return 1000;
      if (d.source === "staging") return false;
      const phase = d.view?.phase ?? d.degraded?.phase ?? "";
      return isTerminal(phase) ? false : 1000;
    },
    // Keep the last good view on screen while degraded (the page dims it and shows the banner).
    placeholderData: (prev) => prev,
  });

  if (status.isPending) return <div className="card"><Skeleton lines={6} /></div>;
  if (status.isError) {
    const err = status.error as ApiError;
    return <div className="card"><Empty>{err.status === 404 ? "This batch was not found." : "Batch details are temporarily unavailable. Try again in a moment."}</Empty></div>;
  }
  const s = status.data!;
  if (s.source === "staging" && s.staging) return <StagedReview summary={s.staging} />;
  return <LiveDetail status={s} />;
}
