// FI portal entry point: branding for this origin, data fetching (TanStack Query), and routes.
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, Navigate, RouterProvider } from "react-router";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import "@fontsource/source-sans-3/400.css";
import "@fontsource/source-sans-3/600.css";
import "@fontsource/source-sans-3/700.css";
import "./styles.css";
import { BrandingProvider } from "./branding";
import { Shell } from "./components/Shell";
import { ToastProvider } from "./components/ui";
import { session } from "./session";
import { Login } from "./pages/Login";
import { Home } from "./pages/Home";
import { BulkUpload } from "./pages/BulkUpload";
import { BatchPage } from "./pages/BatchPage";
import { ApprovalsQueue } from "./pages/ApprovalsQueue";
import { OpsExceptions } from "./pages/OpsExceptions";
import { OpsAccounts } from "./pages/OpsAccounts";

const queryClient = new QueryClient({ defaultOptions: { queries: { retry: 1, refetchOnWindowFocus: false } } });

function HomeOrOps() {
  return session.user()?.role === "BankOps" ? <Navigate to="/ops/exceptions" replace /> : <Home />;
}

const router = createBrowserRouter([
  { path: "/login", element: <Login /> },
  {
    element: <Shell />,
    children: [
      { path: "/", element: <HomeOrOps /> },
      { path: "/payments/bulk-wire", element: <BulkUpload /> },
      { path: "/payments/bulk-wire/:batchId", element: <BatchPage /> },
      { path: "/approvals", element: <ApprovalsQueue /> },
      { path: "/ops/exceptions", element: <OpsExceptions /> },
      { path: "/ops/accounts", element: <OpsAccounts /> },
      { path: "*", element: <Navigate to="/" replace /> },
    ],
  },
]);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrandingProvider>
        <ToastProvider>
          <RouterProvider router={router} />
        </ToastProvider>
      </BrandingProvider>
    </QueryClientProvider>
  </StrictMode>,
);
