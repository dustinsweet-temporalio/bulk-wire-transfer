// Vite build for the FI portal. One build serves both FIs; the gateway's /config.js sets window.FI_ID per origin.
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    // Local `npm run dev` against a running stack: the Lakeshore gateway does the routing.
    proxy: { "/api": "http://localhost:3001", "/config.js": "http://localhost:3001" },
  },
});
