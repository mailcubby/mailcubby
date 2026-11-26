import "@fontsource-variable/inter";
import "./styles/globals.css";

import { MutationCache, QueryCache, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createRouter, RouterProvider } from "@tanstack/react-router";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { NotFoundPage } from "@/components/PageStates";
import { ApiError, handleApiError, handleUnauthorizedRedirect } from "@/lib/api";
import { routeTree } from "./routeTree.gen";

const queryClient = new QueryClient({
  queryCache: new QueryCache({
    onError: (error) => {
      handleUnauthorizedRedirect(error);
    },
  }),
  mutationCache: new MutationCache({
    onError: (error, _variables, _context, mutation) => {
      if (mutation.options.onError) {
        return;
      }
      handleApiError(error);
    },
  }),
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      retry: (failureCount, error) => {
        if (error instanceof ApiError && error.status < 500) {
          return false;
        }
        return failureCount < 1;
      },
    },
  },
});

const router = createRouter({
  routeTree,
  defaultNotFoundComponent: NotFoundPage,
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}

const rootElement = document.getElementById("root");
if (!rootElement) {
  throw new Error("Root element #root not found");
}

createRoot(rootElement).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
);
