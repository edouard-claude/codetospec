; Definitions and imports for TypeScript.

(class_declaration
  name: (type_identifier) @name) @def.class

(function_declaration
  name: (identifier) @name) @def.function

(method_definition
  name: (property_identifier) @name) @def.method

(interface_declaration
  name: (type_identifier) @name) @def.interface

(type_alias_declaration
  name: (type_identifier) @name) @def.type

(enum_declaration
  name: (identifier) @name) @def.enum

(import_statement
  source: (string) @import.target) @import
