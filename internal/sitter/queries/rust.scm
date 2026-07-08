; Definitions, imports and modules for Rust.

(mod_item
  name: (identifier) @module.name) @module

(function_item
  name: (identifier) @name) @def.function

(struct_item
  name: (type_identifier) @name) @def.struct

(enum_item
  name: (type_identifier) @name) @def.enum

(trait_item
  name: (type_identifier) @name) @def.trait

(impl_item
  type: (type_identifier) @name) @def.impl

(use_declaration
  argument: (_) @import.target) @import
