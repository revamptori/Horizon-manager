#include <jni.h>
#include <stdlib.h>
#include <string.h>
#include "_cgo_export.h"

/* JNI's modified UTF-8 is not ordinary UTF-8. Round-trip through Java byte arrays,
 * preserving supplementary Unicode (e.g. emoji) in profiles and console output. */
static char *from_java(JNIEnv *env, jstring value) {
    if (value == NULL) return NULL;
    jclass cls = (*env)->FindClass(env, "java/lang/String");
    jmethodID method = (*env)->GetMethodID(env, cls, "getBytes", "(Ljava/lang/String;)[B");
    jstring charset = (*env)->NewStringUTF(env, "UTF-8");
    jbyteArray bytes = (jbyteArray)(*env)->CallObjectMethod(env, value, method, charset);
    if ((*env)->ExceptionCheck(env) || bytes == NULL) return NULL;
    jsize size = (*env)->GetArrayLength(env, bytes);
    char *result = malloc((size_t)size + 1);
    if (result == NULL) return NULL;
    (*env)->GetByteArrayRegion(env, bytes, 0, size, (jbyte *)result);
    result[size] = '\0';
    return result;
}
static jstring to_java(JNIEnv *env, char *value) {
    if (value == NULL) return NULL;
    size_t size = strlen(value);
    jbyteArray bytes = (*env)->NewByteArray(env, (jsize)size);
    if (bytes == NULL) { HorizonFree(value); return NULL; }
    (*env)->SetByteArrayRegion(env, bytes, 0, (jsize)size, (const jbyte *)value);
    jclass cls = (*env)->FindClass(env, "java/lang/String");
    jmethodID constructor = (*env)->GetMethodID(env, cls, "<init>", "([BLjava/lang/String;)V");
    jstring charset = (*env)->NewStringUTF(env, "UTF-8");
    jstring result = (jstring)(*env)->NewObject(env, cls, constructor, bytes, charset);
    HorizonFree(value);
    return result;
}
JNIEXPORT jstring JNICALL Java_com_horizon_servermanager_Core_nativeInit(JNIEnv *env, jobject self, jstring path) {
    char *input = from_java(env, path); if (input == NULL) return NULL;
    char *result = HorizonInit(input); free(input); return to_java(env, result);
}
JNIEXPORT jstring JNICALL Java_com_horizon_servermanager_Core_call(JNIEnv *env, jobject self, jstring request) {
    char *input = from_java(env, request); if (input == NULL) return NULL;
    char *result = HorizonCall(input); free(input); return to_java(env, result);
}
JNIEXPORT void JNICALL Java_com_horizon_servermanager_Core_shutdown(JNIEnv *env, jobject self) { HorizonShutdown(); }
