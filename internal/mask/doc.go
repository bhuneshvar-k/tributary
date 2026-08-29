// Package mask applies column-level transformers (hash, deterministic
// fake, redact) to rows before they land in the target database. Hashing
// is deterministic so a masked foreign key still resolves correctly
// everywhere it's referenced.
//
// Phase 3 of the project plan.
//
// Not yet implemented.
package mask
