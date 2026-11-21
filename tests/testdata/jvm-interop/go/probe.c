#include <jni.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "_cgo_export.h"

static jint JNICALL read_value(JNIEnv *env, jclass owner, jlong handle) {
    (void) env;
    (void) owner;
    return gpp_probe_read_value(handle);
}

int run_probe(const char *class_path, const char *jar_path, jlong handle, jint *result) {
    const char *prefix = "-Djava.class.path=";
    size_t option_length = strlen(prefix) + strlen(class_path) + 1;
    char *option_text = malloc(option_length);
    if (option_text == NULL) {
        return JNI_ENOMEM;
    }
    snprintf(option_text, option_length, "%s%s", prefix, class_path);
    JavaVMOption options[] = {{.optionString = option_text}};
    JavaVMInitArgs arguments = {
        .version = JNI_VERSION_1_8,
        .nOptions = 1,
        .options = options,
        .ignoreUnrecognized = JNI_FALSE
    };
    JavaVM *vm;
    JNIEnv *env;
    jint status = JNI_CreateJavaVM(&vm, (void **) &env, &arguments);
    free(option_text);
    if (status != JNI_OK) {
        return status;
    }

    jclass base = (*env)->FindClass(env, "port/GoBackedBase");
    if (base == NULL) {
        goto error;
    }
    JNINativeMethod methods[] = {
        {.name = "readValue", .signature = "(J)I", .fnPtr = read_value}
    };
    if ((*env)->RegisterNatives(env, base, methods, 1) != JNI_OK) {
        goto error;
    }
    jclass probe = (*env)->FindClass(env, "port/Probe");
    if (probe == NULL) {
        goto error;
    }
    jmethodID run = (*env)->GetStaticMethodID(env, probe, "run", "(Ljava/lang/String;J)I");
    if (run == NULL) {
        goto error;
    }
    jstring jar = (*env)->NewStringUTF(env, jar_path);
    if (jar == NULL) {
        goto error;
    }
    *result = (*env)->CallStaticIntMethod(env, probe, run, jar, handle);
    if ((*env)->ExceptionCheck(env)) {
        goto error;
    }
    return (*vm)->DestroyJavaVM(vm);

error:
    if ((*env)->ExceptionCheck(env)) {
        (*env)->ExceptionDescribe(env);
    }
    (*vm)->DestroyJavaVM(vm);
    return JNI_ERR;
}
