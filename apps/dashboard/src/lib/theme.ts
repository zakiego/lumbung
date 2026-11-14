export type Theme = "light" | "dark" | "system"

const THEME_KEY = "lumbung.theme"
const SIDEBAR_KEY = "lumbung.sidebar"
// The page background in each theme, for the browser's status bar.
const THEME_COLOR = { light: "#ffffff", dark: "#08090a" }

// Runs in <head> before first paint, so the theme and the sidebar width are
// right on the first frame. Keep it in step with applyTheme and setSidebar.
export const BOOT_SCRIPT = `(function(){try{
var e=document.documentElement;
var t=localStorage.getItem('${THEME_KEY}')||'system';
var d=t==='dark'||(t!=='light'&&matchMedia('(prefers-color-scheme: dark)').matches);
e.classList.add(d?'dark':'light');
e.style.colorScheme=d?'dark':'light';
var m=document.createElement('meta');m.name='theme-color';m.content=d?'${THEME_COLOR.dark}':'${THEME_COLOR.light}';document.head.appendChild(m);
if(localStorage.getItem('${SIDEBAR_KEY}')==='collapsed')e.dataset.sidebar='collapsed';
}catch(x){}})();`

export function readTheme(): Theme {
  try {
    const t = localStorage.getItem(THEME_KEY)
    return t === "light" || t === "dark" ? t : "system"
  } catch {
    return "system"
  }
}

export function applyTheme(theme: Theme) {
  try {
    localStorage.setItem(THEME_KEY, theme)
  } catch {}
  const dark = theme === "dark" || (theme === "system" && matchMedia("(prefers-color-scheme: dark)").matches)
  const root = document.documentElement
  // Without this, every color transition on the page runs at once.
  const pause = document.createElement("style")
  pause.textContent = "*,*::before,*::after{transition:none!important}"
  document.head.appendChild(pause)
  root.classList.toggle("dark", dark)
  root.classList.toggle("light", !dark)
  root.style.colorScheme = dark ? "dark" : "light"
  let meta = document.querySelector<HTMLMetaElement>('meta[name="theme-color"]')
  if (!meta) {
    meta = document.createElement("meta")
    meta.name = "theme-color"
    document.head.appendChild(meta)
  }
  meta.content = dark ? THEME_COLOR.dark : THEME_COLOR.light
  void getComputedStyle(root).opacity
  requestAnimationFrame(() => pause.remove())
}

export function readSidebarCollapsed(): boolean {
  return typeof document !== "undefined" && document.documentElement.dataset.sidebar === "collapsed"
}

export function setSidebarCollapsed(collapsed: boolean) {
  const root = document.documentElement
  if (collapsed) root.dataset.sidebar = "collapsed"
  else delete root.dataset.sidebar
  try {
    localStorage.setItem(SIDEBAR_KEY, collapsed ? "collapsed" : "expanded")
  } catch {}
}
