-- Explicit loopback rehearsal ONLY; @schema@ is supplied as a quoted identifier.
-- Functions cannot be redirected with a caller's search_path. No dynamic row SQL.
CREATE OR REPLACE FUNCTION @schema@.lane_key_lifecycle_dev_v1()
RETURNS trigger LANGUAGE plpgsql VOLATILE SECURITY INVOKER SET search_path = pg_catalog AS $$
DECLARE
    last_epoch bigint;
BEGIN
    -- Enrollment takes an exclusive parent+token row lock. After waiting for
    -- that lock, the lookup needs a fresh statement snapshot (including when
    -- the enrolling transaction inserted a generation but did not edit tokens).
    IF current_setting('transaction_isolation') <> 'read committed' THEN
        RAISE EXCEPTION 'Key lifecycle fixture requires read committed' USING ERRCODE = '55000';
    END IF;
    SELECT epoch INTO last_epoch FROM @schema@.lane_billing_key_enrollments_v1
        WHERE user_id = OLD.user_id AND token_id = OLD.id
        ORDER BY epoch DESC LIMIT 1 FOR UPDATE;
    IF NOT FOUND THEN
        IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
        RETURN NEW;
    END IF;
    -- No safe purge/identity handoff from spent history in this slice. Retain
    -- the original row for precisely targeted accounting, including when soft-deleted.
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'enrolled Key cannot be physically deleted' USING ERRCODE = '55000';
    END IF;
    IF ROW(OLD.id, OLD.user_id, OLD.key, OLD.unlimited_quota, OLD.remain_quota)
       IS DISTINCT FROM ROW(NEW.id, NEW.user_id, NEW.key, NEW.unlimited_quota, NEW.remain_quota) THEN
        RAISE EXCEPTION 'enrolled Key identity or management quota edit requires drain'
            USING ERRCODE = '55000';
    END IF;
    IF ROW(OLD.status, OLD.deleted_at, OLD.expired_time, OLD.model_limits_enabled,
           OLD.model_limits, OLD.allow_ips, OLD."group", OLD.auto_groups,
           OLD.smart_routing, OLD.cross_group_retry, OLD.routing_strategy,
           OLD.no_retry_within_group, OLD.allow_user_quota_query)
       IS DISTINCT FROM ROW(NEW.status, NEW.deleted_at, NEW.expired_time, NEW.model_limits_enabled,
           NEW.model_limits, NEW.allow_ips, NEW."group", NEW.auto_groups,
           NEW.smart_routing, NEW.cross_group_retry, NEW.routing_strategy,
           NEW.no_retry_within_group, NEW.allow_user_quota_query) THEN
        UPDATE @schema@.lane_billing_key_enrollments_v1 SET state = 'revoked'
           WHERE user_id = OLD.user_id AND token_id = OLD.id AND epoch = last_epoch AND state <> 'revoked';
    END IF;
    RETURN NEW;
END $$;
-- The settings writer always lists status/unlimited_quota. Do NOT claim to
-- police raw-only SQL: escrow and ordinary quota arithmetic legitimately write
-- remain_quota/used_quota/cache_revision. They use separate funding authority.
CREATE OR REPLACE TRIGGER lane_key_lifecycle_dev_v1
    BEFORE DELETE OR UPDATE OF id, user_id, key, unlimited_quota, status, deleted_at,
        expired_time, model_limits_enabled, model_limits, allow_ips, "group", auto_groups,
        smart_routing, cross_group_retry, routing_strategy, no_retry_within_group, allow_user_quota_query
    ON @schema@.tokens FOR EACH ROW EXECUTE FUNCTION @schema@.lane_key_lifecycle_dev_v1();
