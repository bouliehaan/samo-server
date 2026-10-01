// A whole-name/token-prefix artist query expresses artist intent. Without this,
// albums gain the same artist match PLUS their title match and crowd the artist
// out of the four best results (e.g. Elvis -> six Elvis Presley albums).
export function artistNameBonus(name, needle) {
  const normalized = (name || '').toLowerCase().replace(/[\p{P}\p{S}]+/gu, ' ').replace(/\s+/g, ' ').trim();
  if (!needle) return 0;
  return normalized === needle || normalized.startsWith(needle + ' ') ? 700 : 0;
}
