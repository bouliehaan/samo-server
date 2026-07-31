// Live server updates over Server-Sent Events.
//
// Read with fetch() rather than EventSource so the bearer token rides in a
// header. EventSource cannot set headers, which would have meant a stream
// token in the query string — the server treats URL-borne credentials as a
// leak vector (Referer, access logs), and a 30-minute token would also break
// the stream on expiry.
//
// The cost of not using EventSource is that reconnection is ours to do. That
// is the loop at the bottom. It is cheap because every event carries a full
// snapshot: a reconnect needs no replay and no Last-Event-ID, it just picks up
// whatever the current state is.

const RECONNECT_BASE_MS = 1000;
const RECONNECT_MAX_MS = 30000;

// parseFrames splits a buffer into complete SSE blocks, returning the parsed
// events and whatever partial block is left over.
//
// Comment frames (": ping") carry no event: line and parse to null, which is
// how heartbeats are ignored.
export function parseFrames(buffer) {
  const events = [];
  let rest = buffer;
  for (;;) {
    const split = rest.indexOf("\n\n");
    if (split === -1) break;
    const block = rest.slice(0, split);
    rest = rest.slice(split + 2);

    let type = "";
    let data = "";
    for (const line of block.split("\n")) {
      if (line.startsWith("event:")) type = line.slice(6).trim();
      else if (line.startsWith("data:")) data = line.slice(5).trim();
    }
    if (!type || !data) continue;
    try {
      events.push({ type, data: JSON.parse(data) });
    } catch {
      // A malformed frame is one lost snapshot; the next supersedes it.
    }
  }
  return { events, rest };
}

// connect opens the stream and keeps it open, calling onEvent for each event
// and onStatus("live"|"down") as the connection comes and goes.
//
// Returns a function that closes the stream and stops reconnecting.
export function connect({ url, token, onEvent, onStatus }) {
  let stopped = false;
  let controller = null;
  let attempt = 0;
  let retryTimer = null;

  async function pump() {
    controller = new AbortController();
    const response = await fetch(url, {
      headers: { Authorization: "Bearer " + token() },
      signal: controller.signal,
    });
    if (!response.ok || !response.body) {
      throw new Error("event stream: HTTP " + response.status);
    }

    attempt = 0; // Connected — the next failure starts its backoff from scratch.
    if (onStatus) onStatus("live");

    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    for (;;) {
      const { value, done } = await reader.read();
      if (done) return;
      // stream:true so a multi-byte character split across two chunks is not
      // decoded as two replacement characters.
      buffer += decoder.decode(value, { stream: true });
      const parsed = parseFrames(buffer);
      buffer = parsed.rest;
      for (const event of parsed.events) {
        if (onEvent) onEvent(event);
      }
    }
  }

  async function loop() {
    while (!stopped) {
      try {
        await pump();
      } catch (err) {
        if (stopped || err.name === "AbortError") return;
      }
      if (stopped) return;
      if (onStatus) onStatus("down");
      // Exponential backoff so a server that is down, restarting, or behind a
      // flapping tunnel is not hammered by every open tab.
      const wait = Math.min(RECONNECT_BASE_MS * 2 ** attempt, RECONNECT_MAX_MS);
      attempt++;
      await new Promise((resolve) => {
        retryTimer = setTimeout(resolve, wait);
      });
    }
  }

  loop();

  return function close() {
    stopped = true;
    if (retryTimer) clearTimeout(retryTimer);
    if (controller) controller.abort();
  };
}
