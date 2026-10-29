// Vite build for the DEMO-ONLY Control Center (served by the gateway on :3100).
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: { proxy: { "/api": "http://localhost:3100" } },
});
