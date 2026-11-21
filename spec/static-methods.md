# GPP Static Methods Specification

## Goal

Add static methods to GPP classes.

Static methods belong to the class namespace rather than to an instance.

They should support use cases such as:

* factory methods
* parsing
* conversion
* named constructors
* utility methods associated with a class
* generated APIs such as `User.FromJSON(...)`

Static methods should lower cleanly to ordinary Go package-level functions.

They are not virtual and do not participate in instance dispatch.

---

# Syntax

Use:

```go
class User {
    static func Guest() *User {
        return User(
            Name: "Guest",
        )
    }

    static func FromString(value string) (*User, error) {
        ...
    }
}
```

Invocation:

```go
user := User.Guest()

user, err := User.FromString(input)
```

---

# Instance vs Static Methods

Instance method:

```go
class User {
    func NameUpper() string {
        return strings.ToUpper(this.Name)
    }
}
```

Called as:

```go
user.NameUpper()
```

Static method:

```go
class User {
    static func Guest() *User {
        return User(Name: "Guest")
    }
}
```

Called as:

```go
User.Guest()
```

---

# No `this`

Static methods do not operate on an instance.

Therefore:

```go
static func Foo() {
    println(this.Name)
}
```

must be a compile error.

Suggested diagnostic:

```text
static method Foo has no `this`
```

---

# Class Metadata

Static methods may refer to the declaring class explicitly:

```go
class User {
    static func Describe() string {
        return User.class.name
    }
}
```

This is valid.

Static methods do not require a special implicit class receiver.

---

# Lowering

A static method should lower to an ordinary package-level Go function.

GPP:

```go
class User {
    static func Guest() *User {
        return User(Name: "Guest")
    }
}
```

Conceptually lowers to:

```go
func __gpp_User_Guest() User {
    return NewUserWithName("Guest")
}
```

The exact generated symbol name is implementation-defined.

The compiler rewrites:

```go
User.Guest()
```

to the generated function call.

---

# No Virtual Dispatch

Static methods are statically resolved.

Given:

```go
class A {
    static func Name() string {
        return "A"
    }
}

class B : A {
    static func Name() string {
        return "B"
    }
}
```

then:

```go
A.Name()
```

calls `A.Name`.

```go
B.Name()
```

calls `B.Name`.

No vtable lookup is involved.

---

# Static Method Shadowing

A subclass may declare a static method with the same signature as a parent
method. The subclass method hides that declaration. Inherited overloads with
different signatures remain available through the subclass.

Example:

```go
class A {
    static func Parse(s string) A {
        ...
    }
}

class B : A {
    static func Parse(s string) B {
        ...
    }
}
```

Then:

```go
A.Parse(...)
B.Parse(...)
```

resolve independently.

This is not polymorphic overriding.

---

# Inherited Lookup

A subclass may access a parent static method if it does not define one itself.

Example:

```go
class A {
    static func Version() int {
        return 1
    }
}

class B : A {
}
```

Allow:

```go
B.Version()
```

to resolve to the inherited `A.Version()` implementation.

The implementation remains statically bound to `A`.

Within a method, constructor, or initializer, an in-scope static method
can also be called by name. Static calls in an inherited body resolve in the
class that declared the body. Where instance and static overloads share a
name, argument types determine the selected member. A static context rejects
a selected instance method. Fixed-arity candidates take precedence over calls
that pack a variadic tail.

GPP retains lexical lookup for Go callables. A local function value or a
package-level callable can use the same name as a static method. A class
qualifier selects the static method explicitly.

---

# Contextual `self` Type

In a static method, lowercase `self` names the type selected at the call site.
This lets a base class declare a static method that accepts or returns the
concrete subclass type without declaring a separate method for every subclass.

```go
class EntityFactory {
    static func Create() *self {
        return new(self)
    }
}

class User : EntityFactory {
    Name string
}

user := User.Create()
user.Name = "Ada"
```

`Create` is defined once on `EntityFactory`; the call through `User` makes `self`
resolve to `User`, so `user` has type `*User`. The compiler treats `self` as an
implicit generic type parameter and supplies the call-site class. It can appear
in static method parameter and result types, and in the method body as a type
argument, as in `Helper[self](...)`.

`self` is a type, while `this` is an instance value. Using `self` does not add
runtime dispatch: the method implementation is still selected statically.

---

# `super` in Static Methods

`super` may be permitted inside a static method to access the next inherited static implementation.

Example:

```go
class A {
    static func Label() string {
        return "A"
    }
}

class B : A {
    static func Label() string {
        return super.Label() + "B"
    }
}
```

This should resolve statically through normal class method-resolution rules.

If the compiler's current `super` implementation only supports instance methods, static `super` may be deferred until the implementation supports it cleanly.

---

# Overloading

If GPP already supports method overloading, static methods should participate in the same overload-resolution rules.

Example:

```go
class User {
    static func Parse(value string) *User {
        ...
    }

    static func Parse(value []byte) *User {
        ...
    }
}
```

Resolution is compile-time.

---

# Visibility

Static methods follow normal capitalization/export rules.

Example:

```go
class User {
    static func Parse(...) ...
    static func helper(...) ...
}
```

`Parse` is exported according to normal GPP package rules.

`helper` remains package-private.

---

# Generics

Static methods may be generic where the language already permits generic functions.

Example:

```go
class Codec {
    static func Decode[T](data []byte) (T, error) {
        ...
    }
}
```

No additional generic semantics are required solely for static methods.

---

# Annotation Interaction

Annotations may generate static methods.

For example:

```go
class User @{encoding.Serializable} {
}
```

may synthesize:

```go
static func FromJSON(data []byte) (*User, error)
static func FromYAML(data []byte) (*User, error)
```

Generated static methods behave exactly like explicitly written static methods.

---

# Reflection / Introspection

Class metadata should distinguish static and instance methods.

Possible descriptor field:

```go
method.static
```

or equivalent.

Potential class APIs:

```go
User.class.methods
User.class.staticMethods
```

Exact surface may follow the existing method descriptor design.

At minimum, static method metadata must preserve:

* name
* owner
* parameters
* returns
* annotations
* static/instance distinction

---

# Name Conflicts

A class may not contain an instance method and static method with an indistinguishable declaration if that creates ambiguous lookup.

Example:

```go
class User {
    func Parse() {}
    static func Parse() {}
}
```

The compiler should either:

1. allow this because `user.Parse()` and `User.Parse()` are unambiguous, or
2. reject it for implementation simplicity.

Preferred behavior:

Allow it.

Instance and class namespaces are naturally distinguished by the call receiver.

---

# Generated Method Conflicts

When another feature generates a static method, such as serialization:

```go
class User @{encoding.Serializable} {
    static func FromJSON(...) ...
}
```

the compiler must not silently overwrite the user's method.

Preferred rule:

* if the user provides a compatible implementation, use the user's implementation
* otherwise report a conflict

Suggested diagnostic:

```text
generated static method User.FromJSON conflicts with user-defined method
```

---

# AST

Static method declarations should be represented as ordinary method declarations with a static flag.

Conceptually:

```text
MethodDecl {
    Name
    Params
    Returns
    Body
    IsStatic bool
    Annotations
}
```

Avoid inventing a separate AST hierarchy unless useful.

---

# Semantic Checks

For static methods:

* `this` is illegal
* instance fields cannot be referenced implicitly
* instance methods cannot be called without an instance
* class-qualified references are valid
* overload resolution remains compile-time
* no vtable slot is generated

---

# Code Generation

Do not generate:

* receiver parameter
* vtable entry
* instance thunk

Generate only a package-level function and class-qualified lookup metadata as needed.

---

# Example

```go
class User {
    Name string

    static func Guest() *User {
        return User(
            Name: "Guest",
        )
    }

    static func Parse(name string) *User {
        return User(
            Name: strings.TrimSpace(name),
        )
    }

    func Greeting() string {
        return "Hello " + this.Name
    }
}
```

Usage:

```go
guest := User.Guest()
user := User.Parse(" Ada ")

fmt.Println(guest.Greeting())
fmt.Println(user.Greeting())
```

---

# Static Fields and Initialization

A class can declare typed, mutable static fields and static initializer blocks.

```go
class Defaults {
    static Port int = 8080
    static Label string
    static {
        Defaults.Label = "http"
    }

    static func Endpoint() string {
        return Defaults.Label
    }
}
```

Static storage starts with the Go zero value. The first GPP constructor call,
static method invocation, or mutable static field read or write initializes the
declaring class. Parent classes initialize before the class's own fields
and blocks, which run in source order. Access through a subclass to an
inherited member initializes its declaring class. Class literals and native
Go struct literals are passive.

Constructor calls initialize the class before evaluating their arguments.
Static method invocations and simple static field assignments evaluate their
arguments or right-hand expression before triggering initialization.

When several goroutines access an uninitialized class, one runs the initializer
and publishes its results to the others. A recursive access from that same
goroutine can observe partially initialized fields. A GPP exception raised
during initialization becomes an `ExceptionInInitializerError` unless it
already represents a Java error. Subsequent access raises
`NoClassDefFoundError` with the original failure in its cause chain. A native
Go panic retains its panic value.

Go callers can use generated static methods and typed field accessors such as
`GppStatic_Defaults_Endpoint`, `GppGetStatic_Defaults_Port`, and
`GppSetStatic_Defaults_Port`. The accessors run the same initialization protocol.

Static fields can also be used by simple name in the class's method and
initializer bodies. The field's declaring class owns its storage, even when a
subclass inherits the code that references it. A local or parameter shadows
the field only in its lexical scope.

A static field initializer or block can assign a later field by simple name.
Reading a later field by simple name, or reading the field being initialized
by simple name, is a compile-time error. A class-qualified reference can
observe the field's current value during initialization.

## Class constants

Use `static const Name Type = expression` to declare a typed class constant.

```go
class Defaults {
    static const Port int = 8080
    static const Scheme string = "http"
    static const Address string = Scheme + "://localhost"
}
```

Constants are available by simple name in the declaring class and by
qualified name through a class or subclass. Their initializer uses Go
constant expression rules and may reference other constants. The compiler
rejects simple-name self-references and forward reads. A qualified forward
reference is valid when the constant dependencies are acyclic.

Reading a constant leaves the class uninitialized, including when read
through a subclass or after an initializer has failed. Go can use exported
class constants as ordinary typed constants, for example
`GppConst_Defaults_Port`.

## Runtime-initialized final fields

Use `static final Name Type = expression` for a field whose value is computed
during class initialization. A declaration without an initializer is a blank
final. Assign it by simple name in a static initializer of the declaring class.
Every path that completes class initialization must assign each blank final
exactly once. The compiler checks reads and assignments across branches,
loops, switch cases, and exception handling. Labeled breaks can exit an
enclosing loop or switch, and labeled continues resume an enclosing loop.

```go
class Settings {
    static final Address string = ReadEnvironment("SERVER_ADDRESS")
    static final Port int
    static {
        if IsDevelopment() {
            Port = 8080
        } else {
            Port = 443
        }
    }
}
```

A final field has the usual typed Go getter, such as
`GppGetStatic_Settings_Address`. Reading it initializes its declaring class.
The field is read-only after initialization. A caller can still use or mutate
the object held by a final reference according to its type.

The `static const` syntax covers compile-time constants. The `static final`
syntax always uses runtime initialization. Java-specific constant folding and interface initialization remain open.
Blank-final analysis currently requires structured branches. Transfers across
an exception handling boundary and Go `goto` in blank-final initialization
remain open.

## Static field reflection

Class metadata exposes static fields through `DeclaredStaticFields()`,
`DeclaredStaticField(name)`, `PublicStaticFields()`, and
`PublicStaticField(name)`. The declared queries inspect the specified class.
The public queries include inherited declarations and retain the declaring
class on each result. The public name lookup starts at the specified class.

Static fields, constants, and runtime-initialized final fields can carry
annotations targeted at fields. The descriptor reports the declared type
and flags for final, constant, and public fields. Metadata lookup alone
leaves the class uninitialized. Reflective `Get()` and `Set(value)` initialize
the declaring class, including reflective reads of constants. `Set` returns an
error for final fields and for values outside the declared Go field type.

---

# Non-goals

Do not add in this feature:

* late-bound static dispatch
* metaclasses
* class objects as runtime receivers
* checked factory semantics

These can be considered separately if needed.

---

# Design Principle

Static methods are:

> class-namespaced package functions

They should provide useful organization and factory syntax without complicating the object model.

The desired mental model is:

```text
instance method
    object behavior
    virtual/polymorphic

static method
    class-associated function
    compile-time/static dispatch
```

They should make classes feel complete without introducing a second runtime dispatch system.
