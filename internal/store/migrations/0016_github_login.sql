-- GitHub sign-in starts before a Forge user exists.
--
-- github_oauth_states.user_id was NOT NULL because 7.7 only connected
-- GitHub to an operator who was already signed in. A login has no
-- user yet, so the row stores NULL. The callback treats NULL as
-- "find or create the operator for this GitHub user id".
-- A non-NULL user_id is still the connect flow and still requires
-- that operator's session cookie.
--
-- NULL is not enough by itself. The login callback also requires the
-- forge_github_state cookie set when the browser started the flow,
-- so a copied callback URL cannot sign a different browser in.

ALTER TABLE github_oauth_states
    ALTER COLUMN user_id DROP NOT NULL;
