-- Source-side fixture for internal/load's tests (phase 2). Generic,
-- non-business-domain table names, per the project's own preference —
-- these describe a *shape* (leaf, parent/child, composite, self-
-- referencing, polymorphic, custom-typed, orphaned), not a scenario.
--
-- The target database starts empty; internal/load.EnsureSchema is
-- expected to stand up whatever it needs from this schema.

CREATE TABLE leaf_table (
    id   int PRIMARY KEY,
    name text NOT NULL
);

CREATE TABLE parent_table (
    id   int PRIMARY KEY,
    name text NOT NULL
);

CREATE TABLE child_table (
    id        int PRIMARY KEY,
    parent_id int NOT NULL REFERENCES parent_table(id),
    name      text NOT NULL
);

-- Composite key: parent keyed (tenant_id, id) rather than a single global
-- id, so two different tenants can have a row with the same local id —
-- this is what makes the composite-FK test meaningful (catches a bug that
-- mismatches column pairing or ignores tenant_id entirely).
CREATE TABLE tenant_table (
    id int PRIMARY KEY
);

CREATE TABLE composite_parent_table (
    tenant_id int NOT NULL REFERENCES tenant_table(id),
    id        int NOT NULL,
    name      text NOT NULL,
    PRIMARY KEY (tenant_id, id)
);

CREATE TABLE composite_child_table (
    id        int PRIMARY KEY,
    parent_id int NOT NULL,
    tenant_id int NOT NULL,
    sku       text NOT NULL,
    FOREIGN KEY (parent_id, tenant_id) REFERENCES composite_parent_table(id, tenant_id)
);

-- Self-referencing, nullable — the null-then-backfill case.
CREATE TABLE self_ref_table (
    id      int PRIMARY KEY,
    next_id int REFERENCES self_ref_table(id)
);

-- Self-referencing, NOT NULL — the unsupported case. A single
-- self-pointing root row (id = next_id) is the only way to seed this
-- without deferring the constraint.
CREATE TABLE self_ref_strict_table (
    id      int PRIMARY KEY,
    next_id int NOT NULL REFERENCES self_ref_strict_table(id)
);

-- Polymorphic targets + source (declared in relations.yaml, no DB
-- constraint on target_id).
CREATE TABLE poly_target_a (
    id   int PRIMARY KEY,
    name text NOT NULL
);

CREATE TABLE poly_target_b (
    id   int PRIMARY KEY,
    name text NOT NULL
);

CREATE TABLE poly_source_table (
    id          int PRIMARY KEY,
    target_type text NOT NULL,
    target_id   int NOT NULL
);

-- Enum type, present on source but deliberately NOT created on target —
-- EnsureSchema auto-creates missing enum types, so loading this table
-- should succeed (see TestEnsureSchema_MissingEnumType_AutoCreated).
CREATE TYPE enum_status AS ENUM ('active', 'inactive');

CREATE TABLE enum_table (
    id     int PRIMARY KEY,
    status enum_status NOT NULL
);

-- Domain type: also USER-DEFINED, but not an enum — Tributary doesn't
-- know how to recreate a domain faithfully (its CHECK constraint), so
-- this is the case that's still a hard preflight error.
CREATE DOMAIN positive_int AS integer CHECK (VALUE > 0);

CREATE TABLE domain_table (
    id     int PRIMARY KEY,
    amount positive_int NOT NULL
);

-- A real FK, with one row's reference deliberately left dangling (trigger
-- disabled around the insert) to simulate pre-existing corrupt source
-- data — the orphaned-FK case. Postgres allows this without revalidating.
CREATE TABLE orphaned_fk_table (
    id     int PRIMARY KEY,
    ref_id int NOT NULL REFERENCES leaf_table(id)
);
