import React from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, HashRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import App from "./App";
import "./index.css";
import { isDemo, installDemo } from "./lib/demo";
import { DemoBanner } from "./components/DemoBanner";

// Demo build: swap in the in-browser mock backend before anything fetches.
if (isDemo()) installDemo();
// GitHub Pages serves under a subpath with no SPA fallback → hash routing in demo.
const Router = isDemo() ? HashRouter : BrowserRouter;

const qc = new QueryClient({
  defaultOptions: {
    queries: {
      refetchOnWindowFocus: false,
      retry: 1,
      staleTime: 4_000,
    },
  },
});

createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <QueryClientProvider client={qc}>
      <Router>
        <App />
      </Router>
      {isDemo() && <DemoBanner />}
    </QueryClientProvider>
  </React.StrictMode>,
);
