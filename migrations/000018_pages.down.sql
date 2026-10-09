-- 000018_pages down: drop what the up migration added.
DROP TABLE IF EXISTS page_revisions;
DROP TABLE IF EXISTS pages;
