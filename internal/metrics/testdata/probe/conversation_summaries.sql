CREATE TABLE conversation_summaries (`conversation_id` text, `last_modified_time` datetime NOT NULL, `workspace_uris` text NOT NULL, `agent_name` text NOT NULL DEFAULT '', `parent_conversation_id` text NOT NULL DEFAULT '');
INSERT INTO conversation_summaries VALUES ('6a0066be-3958-4af9-9319-a4544694a20f', '2026-11-03 18:31:27.339896+00:00', '["file:///workspace/probe"]', '', '');
INSERT INTO conversation_summaries VALUES ('c3882232-59f6-4709-b3cf-f7feec3e998e', '2026-11-03 18:31:24.968306+00:00', '["file:///workspace/probe"]', 'research', '6a0066be-3958-4af9-9319-a4544694a20f');
INSERT INTO conversation_summaries VALUES ('c321274c-a20a-4366-b7ff-dcb2a91da5e0', '2026-11-03 18:24:03.482709+00:00', '["file:///workspace/probe"]', '', '');
INSERT INTO conversation_summaries VALUES ('4315b739-38a8-4df5-9f4a-bfef0ecd0635', '2026-11-03 18:24:17.864311+00:00', '["file:///workspace/probe"]', '', '');
