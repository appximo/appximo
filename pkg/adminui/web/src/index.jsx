import { render } from "solid-js/web"
import "./styles/global.css"
import "./styles/components.css"
import { initTheme } from "./components/ui"
import App from "./App"

initTheme()
render(() => <App />, document.getElementById("root"))
