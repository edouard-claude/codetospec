; Definitions, imports and modules for PHP.

(namespace_definition
  name: (namespace_name) @module.name) @module

(class_declaration
  name: (name) @name) @def.class

(interface_declaration
  name: (name) @name) @def.interface

(trait_declaration
  name: (name) @name) @def.trait

(enum_declaration
  name: (name) @name) @def.enum

(function_definition
  name: (name) @name) @def.function

(method_declaration
  (visibility_modifier)? @visibility
  name: (name) @name) @def.method

(namespace_use_clause
  [(name) (qualified_name)] @import.target) @import
