import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Dev server port matches CORS_ALLOWED_ORIGINS in .env.example — change
// both together if this ever needs to move.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
  },
});
