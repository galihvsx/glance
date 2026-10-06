-- 000010_satellites down: drop satellite tables in reverse dependency order.
DROP TABLE IF EXISTS issue_versions;
DROP TABLE IF EXISTS issue_relations;
DROP TABLE IF EXISTS issue_subscribers;
DROP TABLE IF EXISTS issue_votes;
DROP TABLE IF EXISTS issue_reactions;
DROP TABLE IF EXISTS comment_reactions;
DROP TABLE IF EXISTS comments;
