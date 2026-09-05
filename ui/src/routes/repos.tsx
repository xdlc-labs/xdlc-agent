import { Outlet, createFileRoute } from "@tanstack/react-router";

// Layout for /repos and /repos/$id — child routes render via Outlet.
export const Route = createFileRoute("/repos")({
  component: ReposLayout,
});

function ReposLayout() {
  return <Outlet />;
}
