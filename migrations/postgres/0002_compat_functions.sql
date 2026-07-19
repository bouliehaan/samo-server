-- Add PostgreSQL compatibility function for json_each(text).
-- This handles orphan pruning scans that use the SQLite table-valued json_each.
CREATE OR REPLACE FUNCTION json_each(j text) RETURNS TABLE(value text)
LANGUAGE sql IMMUTABLE AS $fn$
  SELECT jsonb_array_elements_text((CASE WHEN j = '' THEN '[]' ELSE j END)::jsonb);
$fn$;
