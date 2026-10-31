// Keep redirects on this server, preserve pairing/player links, and never land
// a listener in the administration dashboard. Everyone lands in the player by
// default; an admin reaches the dashboard from its "Server admin" link, or by
// a link that asked for /app.
export function loginDestination(user, next, origin) {
  const admin = user && user.role === "admin";
  try {
    if (next) {
      const target = new URL(next, origin);
      const path = target.pathname;
      const allowed = path === "/pair" || path === "/pair/" || path === "/listen/" ||
        (admin && (path === "/app" || path === "/app/"));
      if (target.origin === origin && allowed) return path + target.search + target.hash;
    }
  } catch { /* Invalid destinations use the role's default landing page. */ }
  return "/listen/";
}
