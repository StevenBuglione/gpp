package main

/*
#include <stdlib.h>
#include <jni.h>

int run_probe(const char *class_path, const char *jar_path, jlong handle, jint *result);
*/
import "C"

import (
	"fmt"
	"os"
	"runtime/cgo"
	"unsafe"
)

type bean struct {
	value int
	calls int
}

//export gpp_probe_read_value
func gpp_probe_read_value(handle C.jlong) C.jint {
	value := cgo.Handle(handle).Value().(*bean)
	value.calls++
	return C.jint(value.value)
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: jvm-probe <base class directory> <extension jar>")
		os.Exit(2)
	}
	if err := probe(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func probe(classPath, jarPath string) error {
	javaClassPath := C.CString(classPath)
	defer C.free(unsafe.Pointer(javaClassPath))
	javaJarPath := C.CString(jarPath)
	defer C.free(unsafe.Pointer(javaJarPath))

	value := &bean{value: 40}
	handle := cgo.NewHandle(value)
	defer handle.Delete()

	var result C.jint
	if status := C.run_probe(javaClassPath, javaJarPath, C.jlong(handle), &result); status != 0 {
		return fmt.Errorf("JVM callback probe failed with JNI status %d", status)
	}
	if result != 84 || value.calls != 2 {
		return fmt.Errorf("got result %d and %d Go callbacks, want 84 and 2", result, value.calls)
	}
	fmt.Println("JVM extension subclasses called the Go implementation through two class loaders")
	return nil
}
