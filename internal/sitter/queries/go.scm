; Definitions, imports and modules for Go.

(package_clause
  (package_identifier) @module.name) @module

(function_declaration
  name: (identifier) @name) @def.function

(method_declaration
  name: (field_identifier) @name) @def.method

(type_declaration
  (type_spec
    name: (type_identifier) @name)) @def.type

(import_spec
  path: (interpreted_string_literal) @import.target) @import
