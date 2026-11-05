// TSK-001 scaffold router: TanStack Router (Solid adapter) with hash history.
//
// Hash history is deliberate until the SPA asset-server middleware lands (TSK-603):
// the Wails asset server does not rewrite deep links in production builds.
//
// Route table mirrors the original app's router (docs/implementation-plan.md §5/F2).
// Phase 2 owns real implementations (TSK-202 shell chrome, TSK-203 home/search views).
import { Link, Outlet, RouterProvider, createHashHistory, createRootRoute, createRoute, createRouter } from '@tanstack/solid-router';
import type { Component } from 'solid-js';

function RootLayout() {
  // TSK-202 replaces this stub with the real Shell (NavRail / TopBar / page viewport).
  return (
    <div class="flex h-full flex-col">
      <nav class="navbar bg-base-100 shadow-sm">
        <span class="btn btn-ghost text-lg font-semibold">Chordly</span>
        <div class="ml-auto flex gap-2">
          <Link to="/" class="btn btn-ghost btn-sm">
            Home
          </Link>
          <Link to="/search" class="btn btn-ghost btn-sm">
            Search
          </Link>
        </div>
      </nav>
      <main class="min-h-0 flex-1 overflow-auto">
        <Outlet />
      </main>
    </div>
  );
}

function HomeStub() {
  return (
    <div class="grid h-full place-items-center p-8 text-center">
      <div class="max-w-md">
        <h1 class="text-2xl font-bold">Chordly</h1>
        <p class="mt-2 text-base-content/70">
          Wails port scaffold running. Home feed implementation lands in TSK-203.
        </p>
      </div>
    </div>
  );
}

function SearchStub() {
  return (
    <div class="p-8">
      <h1 class="text-xl font-bold">Search</h1>
      <p class="mt-2 text-base-content/70">Search view implementation lands in TSK-203.</p>
    </div>
  );
}

function NotFound() {
  return (
    <div class="grid h-full place-items-center p-8 text-center">
      <div>
        <h1 class="text-4xl font-black">404</h1>
        <p class="mt-2 text-base-content/70">This page does not exist.</p>
        <Link to="/" class="btn btn-primary btn-sm mt-4">
          Go home
        </Link>
      </div>
    </div>
  );
}

const hashHistory = createHashHistory();

const rootRoute = createRootRoute({
  component: RootLayout,
  notFoundComponent: NotFound,
});

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  component: HomeStub,
});

const searchRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/search',
  component: SearchStub,
});

const routeTree = rootRoute.addChildren([indexRoute, searchRoute]);

export const router = createRouter({
  routeTree,
  history: hashHistory,
});

// Module augmentation: registers the router instance for typed `Link`s / `navigate`.
declare module '@tanstack/solid-router' {
  interface Register {
    router: typeof router;
  }
}

export const AppRouter: Component = () => <RouterProvider router={router} />;
