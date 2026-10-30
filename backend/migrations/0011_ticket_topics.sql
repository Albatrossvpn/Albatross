-- The topic a ticket has in the team's group when the group has Topics:
-- the person's messages go there and the team answers there (0: none).
ALTER TABLE tg_tickets ADD COLUMN topic INTEGER NOT NULL DEFAULT 0;
