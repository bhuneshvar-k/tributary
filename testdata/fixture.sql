-- Fixture schema for internal/graph's closure tests (phase 1). Covers, in
-- one schema: a composite FK, a self-referencing (cyclic) table, a soft FK
-- with no DB constraint, a polymorphic association, and a real FK meant to
-- be ignored — see testdata/relations.yaml for the declared side of these.

create table tenants (
    id int primary key
);

-- orders is keyed (tenant_id, id) rather than a single global id, so two
-- different tenants can have an order with the same local id — this is
-- what makes the line_items composite FK test meaningful: a bug that
-- mismatches (order_id, tenant_id) <-> (id, tenant_id) column pairing (or
-- ignores tenant_id entirely) would wrongly cross a line_item into the
-- other tenant's identically-numbered order.
create table orders (
    tenant_id int not null references tenants(id),
    id int not null,
    user_id int not null,
    primary key (tenant_id, id)
);

create table users (
    id int primary key
);

create table line_items (
    order_id int not null,
    tenant_id int not null,
    sku text not null,
    foreign key (order_id, tenant_id) references orders(id, tenant_id)
);

-- Self-referencing: needs a dependency_breaks entry (or best-effort
-- auto-break) to avoid an unbounded management-chain walk.
create table employees (
    id int primary key,
    manager_id int references employees(id)
);

create table posts (
    id int primary key
);

create table photos (
    id int primary key
);

-- Polymorphic: commentable_type/commentable_id has no DB-level equivalent,
-- declared only in testdata/relations.yaml.
create table comments (
    id int primary key,
    commentable_type text not null,
    commentable_id int not null
);

-- Real FK meant to be suppressed via `ignore:` in testdata/relations.yaml
-- (an audit table that would otherwise pull in every user who ever acted,
-- not just the seeded one).
create table audit_logs (
    id int primary key,
    actor_id int not null references users(id)
);
