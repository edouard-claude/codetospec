; Definitions and imports for JavaScript.

(class_declaration
  name: (identifier) @name) @def.class

(function_declaration
  name: (identifier) @name) @def.function

(method_definition
  name: (property_identifier) @name) @def.method

(import_statement
  source: (string) @import.target) @import
