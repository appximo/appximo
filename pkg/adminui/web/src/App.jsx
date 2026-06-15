import { Show } from "solid-js"
import { HashRouter, Route, Navigate } from "@solidjs/router"
import { Shell } from "./shell/Shell"
import { Login } from "./routes/Login"
import { Tenants } from "./routes/Tenants"
import { Users } from "./routes/Users"
import { Data } from "./routes/Data"
import { isAuthed } from "./lib/auth"
import { Toaster } from "./components/ui"

// Layout is the Router `root`: it wraps every route. Unauthenticated → the Login
// screen (no shell). Authenticated → the Shell with the routed page inside. The
// `isAuthed()` signal makes this reactive: logging in/out swaps the tree.
function Layout(props) {
  return (
    <>
      <Show when={isAuthed()} fallback={<Login />}>
        <Shell>{props.children}</Shell>
      </Show>
      <Toaster />
    </>
  )
}

// Hash routing (/admin#/tenants): client routes live in the URL fragment, so they
// never reach the Go server and never collide with the /admin/* ADMIN-API-V1 routes.
export default function App() {
  return (
    <HashRouter root={Layout}>
      <Route path="/" component={() => <Navigate href="/tenants" />} />
      <Route path="/login" component={() => <Navigate href="/tenants" />} />
      <Route path="/tenants" component={Tenants} />
      <Route path="/users" component={Users} />
      <Route path="/data" component={Data} />
      {/* /observability is ADMIN-UI-V2. */}
      <Route path="*" component={() => <Navigate href="/tenants" />} />
    </HashRouter>
  )
}
