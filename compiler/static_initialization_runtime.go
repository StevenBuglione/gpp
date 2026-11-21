package compiler

import "strings"

// The runtime owns the initialization monitor and publishes completed writes
// before other goroutines can read a field. Generated code supplies the
// exception ABI used by its package.
func classInitializationRuntime(runtimeAlias, syncAlias, atomicAlias, typePrefix string) string {
	source := `
type gppInitTypeClassInitialization struct {
    ready gppInitAtomic.Bool
    mutex gppInitSync.Mutex
    pending chan struct{}
    owner uint64
    failure error
    nativePanic any
}

type gppInitTypeExceptionInInitializerError struct {
    Class string
    Cause error
}

func (failure *gppInitTypeExceptionInInitializerError) Error() string {
    return "exception initializing " + failure.Class + ": " + failure.Cause.Error()
}
func (failure *gppInitTypeExceptionInInitializerError) Unwrap() error { return failure.Cause }
func (*gppInitTypeExceptionInInitializerError) JavaClassName() string { return "java.lang.ExceptionInInitializerError" }
func (*gppInitTypeExceptionInInitializerError) JavaError() {}

type gppInitTypeNoClassDefFoundError struct {
    Class string
    Cause error
}

func (failure *gppInitTypeNoClassDefFoundError) Error() string { return "could not initialize class " + failure.Class }
func (failure *gppInitTypeNoClassDefFoundError) Unwrap() error { return failure.Cause }
func (*gppInitTypeNoClassDefFoundError) JavaClassName() string { return "java.lang.NoClassDefFoundError" }
func (*gppInitTypeNoClassDefFoundError) JavaError() {}

func (state *gppInitTypeClassInitialization) Ensure(class string, initialize func(), unwrap func(any) (error, bool), raise func(error)) {
    if state.ready.Load() { return }
    for {
        state.mutex.Lock()
        if state.ready.Load() {
            state.mutex.Unlock()
            return
        }
        if state.failure != nil {
            cause := state.failure
            state.mutex.Unlock()
            raise(&gppInitTypeNoClassDefFoundError{Class: class, Cause: cause})
            return
        }
        if state.nativePanic != nil {
            recovered := state.nativePanic
            state.mutex.Unlock()
            panic(recovered)
        }
        // The supported Go runtime identifies the current goroutine in the
        // header for Stack(..., false). Use it only on the initialization
        // slow path, where recursion must be distinguished from a waiter.
        owner := __gppClassInitGoroutine()
        if owner == 0 {
            state.mutex.Unlock()
            panic("GPP class initialization could not identify the current goroutine")
        }
        if pending := state.pending; pending != nil {
            recursive := state.owner == owner
            state.mutex.Unlock()
            if recursive { return }
            <-pending
            continue
        }
        state.pending = make(chan struct{})
        state.owner = owner
        state.mutex.Unlock()
        break
    }

    complete := false
    defer func() {
        recovered := recover()
        var failure error
        if !complete {
            if thrown, ok := unwrap(recovered); ok {
                if _, isJavaError := thrown.(interface{ JavaError() }); isJavaError {
                    failure = thrown
                } else {
                    failure = &gppInitTypeExceptionInInitializerError{Class: class, Cause: thrown}
                }
            }
        }
        state.mutex.Lock()
        if complete {
            state.ready.Store(true)
        } else if failure != nil {
            state.failure = failure
        } else if recovered != nil {
            state.nativePanic = recovered
        } else {
            // Goexit runs deferred functions without an active panic.
            state.nativePanic = "goroutine exited during class initialization: " + class
        }
        close(state.pending)
        state.pending = nil
        state.owner = 0
        state.mutex.Unlock()
        if failure != nil { raise(failure) }
        if recovered != nil { panic(recovered) }
    }()
    initialize()
    complete = true
}

func __gppClassInitGoroutine() uint64 {
    var header [64]byte
    size := gppInitRuntime.Stack(header[:], false)
    const prefix = "goroutine "
    if size <= len(prefix) || string(header[:len(prefix)]) != prefix { return 0 }
    var id uint64
    for _, digit := range header[len(prefix):size] {
        if digit == ' ' { return id }
        if digit < '0' || digit > '9' { return 0 }
        id = id * 10 + uint64(digit - '0')
    }
    return 0
}
`
	return strings.NewReplacer(
		"gppInitRuntime", runtimeAlias,
		"gppInitSync", syncAlias,
		"gppInitAtomic", atomicAlias,
		"gppInitType", typePrefix,
	).Replace(source)
}
