package main

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation -framework UniformTypeIdentifiers
#include <Security/Security.h>
#include <stdlib.h>
#include <string.h>

static CFMutableDictionaryRef traceQuery(const char *service) {
 CFMutableDictionaryRef q=CFDictionaryCreateMutable(NULL,0,&kCFTypeDictionaryKeyCallBacks,&kCFTypeDictionaryValueCallBacks);
 CFDictionarySetValue(q,kSecClass,kSecClassGenericPassword);
 CFStringRef serviceName=service ? CFStringCreateWithCString(NULL,service,kCFStringEncodingUTF8) : CFSTR("org.0xmhha.trace-agent");
 CFDictionarySetValue(q,kSecAttrService,serviceName);
 if(service)CFRelease(serviceName);
 CFDictionarySetValue(q,kSecAttrAccount,service ? CFSTR("sender") : CFSTR("local-collector"));
 return q;
}
static OSStatus traceLoad(void **bytes, size_t *n) {
 CFMutableDictionaryRef q=traceQuery(0); CFTypeRef result=NULL;
 CFDictionarySetValue(q,kSecReturnData,kCFBooleanTrue);
 OSStatus status=SecItemCopyMatching(q,&result); CFRelease(q);
 if(status!=errSecSuccess)return status;
 CFDataRef d=(CFDataRef)result;
 *n=CFDataGetLength(d); *bytes=malloc(*n);
 if(!*bytes){CFRelease(result);return errSecAllocate;}
 memcpy(*bytes,CFDataGetBytePtr(d),*n);CFRelease(result);return errSecSuccess;
}
static OSStatus traceSave(const void *bytes, size_t n, const char *service) {
 CFMutableDictionaryRef q=traceQuery(service), fields=CFDictionaryCreateMutable(NULL,0,&kCFTypeDictionaryKeyCallBacks,&kCFTypeDictionaryValueCallBacks);
 CFDataRef d=CFDataCreate(NULL,bytes,n);
 CFDictionarySetValue(fields,kSecValueData,d);
 OSStatus status=SecItemUpdate(q,fields);
 if(status==errSecItemNotFound){
  CFDictionarySetValue(q,kSecValueData,d);
  CFDictionarySetValue(q,kSecAttrAccessible,kSecAttrAccessibleWhenUnlockedThisDeviceOnly);
  status=SecItemAdd(q,NULL);
 }
 CFRelease(d);CFRelease(fields);CFRelease(q);return status;
}
*/
import "C"
import (
	"encoding/json"
	"errors"
	"unsafe"
)

// Keychain failures. They reach the process log at start-up or are mapped to
// a bridge code by the caller, so they are plain English sentences.
var (
	errKeychainRead      = errors.New("cannot read the keychain")
	errKeychainWrite     = errors.New("cannot save sources to the keychain")
	errRegistryTooLarge  = errors.New("stored source registry exceeds its size limit")
	errRegistryInvalid   = errors.New("stored source registry is invalid")
	errCredentialInvalid = errors.New("receiver credential is invalid")
	errCredentialWrite   = errors.New("cannot save the receiver credential to the keychain")
)

type keychain struct{}

func (keychain) Load() (registry, error) {
	var p unsafe.Pointer
	var n C.size_t
	s := C.traceLoad(&p, &n)
	if s == C.errSecItemNotFound {
		return registry{}, nil
	}
	if s != C.errSecSuccess {
		return registry{}, errKeychainRead
	}
	defer C.free(p)
	if n > 1<<20 {
		return registry{}, errRegistryTooLarge
	}
	var r registry
	if json.Unmarshal(C.GoBytes(p, C.int(n)), &r) != nil || len(r.Key) != 32 {
		return registry{}, errRegistryInvalid
	}
	return r, nil
}
func (keychain) Save(r registry) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return errRegistryTooLarge
	}
	if C.traceSave(unsafe.Pointer(&data[0]), C.size_t(len(data)), nil) != C.errSecSuccess {
		return errKeychainWrite
	}
	return nil
}

func saveSenderCredential(service, credential string) error {
	data := []byte(credential)
	if len(data) != 64 {
		return errCredentialInvalid
	}
	name := C.CString(service)
	defer C.free(unsafe.Pointer(name))
	if C.traceSave(unsafe.Pointer(&data[0]), C.size_t(len(data)), name) != C.errSecSuccess {
		return errCredentialWrite
	}
	return nil
}
