-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION track_learning_route_version() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE owner UUID; route UUID;
BEGIN
 IF TG_OP='UPDATE' AND to_jsonb(NEW)=to_jsonb(OLD) THEN RETURN NULL; END IF;
 IF TG_TABLE_NAME='learning_routes' THEN
  IF TG_OP='DELETE' THEN owner:=OLD.user_id; ELSE owner:=NEW.user_id; END IF;
 ELSE
  IF TG_OP='DELETE' THEN route:=OLD.route_id; ELSE route:=NEW.route_id; END IF;
  SELECT user_id INTO owner FROM learning_routes WHERE id=route;
 END IF;
 IF owner IS NOT NULL THEN PERFORM bump_resource_version(owner,'learning_routes'); END IF;
 RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER route_revision AFTER INSERT OR UPDATE OR DELETE ON learning_routes FOR EACH ROW EXECUTE FUNCTION track_learning_route_version();
CREATE TRIGGER route_revision AFTER INSERT OR UPDATE OR DELETE ON learning_route_versions FOR EACH ROW EXECUTE FUNCTION track_learning_route_version();
-- +goose Down
DROP TRIGGER route_revision ON learning_route_versions;
DROP TRIGGER route_revision ON learning_routes;
DROP FUNCTION track_learning_route_version();
