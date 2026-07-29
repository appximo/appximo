// The SPA talks to the API on the SAME origin — no CORS, no second deploy.
// The Bearer token would come from POST /auth/login in a real app.
const token = localStorage.getItem("token") || "";
fetch("/api/tasks", { headers: token ? { Authorization: `Bearer ${token}` } : {} })
  .then((r) => (r.ok ? r.json() : { data: [] }))
  .then(({ data }) => {
    document.getElementById("list").innerHTML =
      data.map((t) => `<li>${t.title}</li>`).join("") || "<li><em>no tasks yet</em></li>";
  })
  .catch(() => {});
