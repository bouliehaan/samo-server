export const providerName = (provider) => ({youtube: "YouTube (yt-dlp)", slskd: "Soulseek", lidarr: "Lidarr"}[provider] || "Explo");
// Explo is still downloading: worth polling quickly.
export const activeJob = (job) => ["queued", "downloading"].includes(job.state);
// Samo's part once the file is staged: identifying, in-library or needs-review.
export const libraryState = (job) => (job.library && job.library.state) || (job.state === "staged" ? "identifying" : "");
// Anything that still moves on its own, so polling continues until it settles.
export const openJob = (job) => activeJob(job) || (job.state === "staged" && libraryState(job) === "identifying");
export function jobLabel(job) {
  if (!job) return "ADD SONG";
  if (job.state === "failed") return "RETRY";
  if (job.state === "staged") return {"in-library": "IN LIBRARY", "needs-review": "NEEDS REVIEW"}[libraryState(job)] || "IDENTIFYING…";
  return {queued: "QUEUED", downloading: "DOWNLOADING…"}[job.state] || "QUEUED";
}
export function jobStatus(job) {
  const provider = providerName(job.provider);
  if (job.state === "queued") return `Waiting for a download slot · ${provider}`;
  if (job.state === "downloading") return job.message || `${provider}: finding and downloading audio…`;
  if (job.state === "staged") {
    const library = job.library || {};
    return library.message || job.message ||
      ({"in-library": "In your library", "needs-review": "Needs review · it is in Explore"}[library.state] || "Downloaded · identifying");
  }
  return job.message || ({failed: "Download failed. Retry with another provider."}[job.state] || job.state);
}
