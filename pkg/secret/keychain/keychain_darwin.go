//go:build darwin

package keychain

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"

	"scoped-mcp/pkg/secret"
)

const (
	coreFoundationPath = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"
	securityPath       = "/System/Library/Frameworks/Security.framework/Security"

	cfStringEncodingUTF8        uint32 = 0x08000100
	errorMessageBufferSize             = 512
	keychainSettingsVersion     uint32 = 1
	noLockInterval              uint32 = math.MaxInt32
	keychainSettingsCLayoutSize        = 12
)

type cfRef = uintptr

type keychainSettings struct {
	version         uint32
	lockOnSleep     bool
	useLockInterval bool
	lockInterval    uint32
}

type keychainSettingsMatchesCLayout [keychainSettingsCLayoutSize]struct{}

var _ keychainSettingsMatchesCLayout = [unsafe.Sizeof(keychainSettings{})]struct{}{}

type securityAPI struct {
	cfStringCreateWithCString func(allocator cfRef, value string, encoding uint32) cfRef
	cfStringGetCString        func(value cfRef, buffer *byte, size int, encoding uint32) bool
	cfDataCreate              func(allocator cfRef, bytes *byte, length int) cfRef
	cfDataGetLength           func(data cfRef) int
	cfDataGetBytePtr          func(data cfRef) *byte
	cfDictionaryCreate        func(allocator cfRef, keys, values *cfRef, count int, keyCallBacks, valueCallBacks uintptr) cfRef
	cfArrayCreate             func(allocator cfRef, values *cfRef, count int, callBacks uintptr) cfRef
	cfArrayGetCount           func(array cfRef) int
	cfReleaseNonNull          func(ref cfRef)

	secKeychainOpen                      func(path string, keychain *cfRef) int32
	secKeychainGetStatus                 func(keychain cfRef, status *uint32) int32
	secKeychainCreate                    func(path string, passwordLength uint32, password *byte, promptUser bool, initialAccess cfRef, keychain *cfRef) int32
	secKeychainSetSettings               func(keychain cfRef, settings *keychainSettings) int32
	secKeychainCopySearchList            func(searchList *cfRef) int32
	secKeychainSetSearchList             func(searchList cfRef) int32
	secKeychainSetUserInteractionAllowed func(allowed bool) int32
	secKeychainLock                      func(keychain cfRef) int32
	secItemAdd                           func(attributes cfRef, result *cfRef) int32
	secItemUpdate                        func(query, attributesToUpdate cfRef) int32
	secItemCopyMatching                  func(query cfRef, result *cfRef) int32
	secItemDelete                        func(query cfRef) int32
	secCopyErrorMessageString            func(status int32, reserved uintptr) cfRef

	cfTypeDictionaryKeyCallBacks   uintptr
	cfTypeDictionaryValueCallBacks uintptr
	cfTypeArrayCallBacks           uintptr
	cfBooleanTrue                  cfRef

	secClass                cfRef
	secClassGenericPassword cfRef
	secAttrService          cfRef
	secAttrAccount          cfRef
	secAttrLabel            cfRef
	secValueData            cfRef
	secUseKeychain          cfRef
	secMatchSearchList      cfRef
	secMatchLimit           cfRef
	secMatchLimitOne        cfRef
	secReturnData           cfRef
}

type loadedFramework struct {
	handle uintptr
	err    error
}

func openFramework(path string) loadedFramework {
	handle, err := purego.Dlopen(path, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return loadedFramework{err: fmt.Errorf("keychain: load %s: %w", path, err)}
	}
	return loadedFramework{handle: handle}
}

func (framework *loadedFramework) bindFunc(fptr any, name string) {
	if framework.err != nil {
		return
	}
	purego.RegisterLibFunc(fptr, framework.handle, name)
}

func (framework *loadedFramework) symbolAddress(name string) uintptr {
	if framework.err != nil {
		return 0
	}
	address, err := purego.Dlsym(framework.handle, name)
	if err != nil {
		framework.err = fmt.Errorf("keychain: symbol %s: %w", name, err)
		return 0
	}
	return address
}

func (framework *loadedFramework) loadConstant(name string) cfRef {
	address := framework.symbolAddress(name)
	if address == 0 {
		return 0
	}
	return **(**cfRef)(unsafe.Pointer(&address))
}

var loadSecurityAPI = sync.OnceValues(func() (*securityAPI, error) {
	coreFoundation := openFramework(coreFoundationPath)
	security := openFramework(securityPath)
	api := &securityAPI{}
	coreFoundation.bindFunc(&api.cfStringCreateWithCString, "CFStringCreateWithCString")
	coreFoundation.bindFunc(&api.cfStringGetCString, "CFStringGetCString")
	coreFoundation.bindFunc(&api.cfDataCreate, "CFDataCreate")
	coreFoundation.bindFunc(&api.cfDataGetLength, "CFDataGetLength")
	coreFoundation.bindFunc(&api.cfDataGetBytePtr, "CFDataGetBytePtr")
	coreFoundation.bindFunc(&api.cfDictionaryCreate, "CFDictionaryCreate")
	coreFoundation.bindFunc(&api.cfArrayCreate, "CFArrayCreate")
	coreFoundation.bindFunc(&api.cfArrayGetCount, "CFArrayGetCount")
	coreFoundation.bindFunc(&api.cfReleaseNonNull, "CFRelease")
	security.bindFunc(&api.secKeychainOpen, "SecKeychainOpen")
	security.bindFunc(&api.secKeychainGetStatus, "SecKeychainGetStatus")
	security.bindFunc(&api.secKeychainCreate, "SecKeychainCreate")
	security.bindFunc(&api.secKeychainSetSettings, "SecKeychainSetSettings")
	security.bindFunc(&api.secKeychainCopySearchList, "SecKeychainCopySearchList")
	security.bindFunc(&api.secKeychainSetSearchList, "SecKeychainSetSearchList")
	security.bindFunc(&api.secKeychainSetUserInteractionAllowed, "SecKeychainSetUserInteractionAllowed")
	security.bindFunc(&api.secKeychainLock, "SecKeychainLock")
	security.bindFunc(&api.secItemAdd, "SecItemAdd")
	security.bindFunc(&api.secItemUpdate, "SecItemUpdate")
	security.bindFunc(&api.secItemCopyMatching, "SecItemCopyMatching")
	security.bindFunc(&api.secItemDelete, "SecItemDelete")
	security.bindFunc(&api.secCopyErrorMessageString, "SecCopyErrorMessageString")
	api.cfTypeDictionaryKeyCallBacks = coreFoundation.symbolAddress("kCFTypeDictionaryKeyCallBacks")
	api.cfTypeDictionaryValueCallBacks = coreFoundation.symbolAddress("kCFTypeDictionaryValueCallBacks")
	api.cfTypeArrayCallBacks = coreFoundation.symbolAddress("kCFTypeArrayCallBacks")
	api.cfBooleanTrue = coreFoundation.loadConstant("kCFBooleanTrue")
	api.secClass = security.loadConstant("kSecClass")
	api.secClassGenericPassword = security.loadConstant("kSecClassGenericPassword")
	api.secAttrService = security.loadConstant("kSecAttrService")
	api.secAttrAccount = security.loadConstant("kSecAttrAccount")
	api.secAttrLabel = security.loadConstant("kSecAttrLabel")
	api.secValueData = security.loadConstant("kSecValueData")
	api.secUseKeychain = security.loadConstant("kSecUseKeychain")
	api.secMatchSearchList = security.loadConstant("kSecMatchSearchList")
	api.secMatchLimit = security.loadConstant("kSecMatchLimit")
	api.secMatchLimitOne = security.loadConstant("kSecMatchLimitOne")
	api.secReturnData = security.loadConstant("kSecReturnData")
	if coreFoundation.err != nil {
		return nil, coreFoundation.err
	}
	if security.err != nil {
		return nil, security.err
	}
	return api, nil
})

func (api *securityAPI) cfRelease(ref cfRef) {
	if ref != 0 {
		api.cfReleaseNonNull(ref)
	}
}

type dictionaryBuilder struct {
	api    *securityAPI
	keys   []cfRef
	values []cfRef
	owned  []cfRef
}

func (api *securityAPI) newDictionaryBuilder() *dictionaryBuilder {
	return &dictionaryBuilder{api: api}
}

func (builder *dictionaryBuilder) add(key, value cfRef) {
	builder.keys = append(builder.keys, key)
	builder.values = append(builder.values, value)
}

func (builder *dictionaryBuilder) addOwned(key, value cfRef) {
	builder.add(key, value)
	builder.owned = append(builder.owned, value)
}

func (builder *dictionaryBuilder) addString(key cfRef, value string) {
	builder.addOwned(key, builder.api.cfStringCreateWithCString(0, value, cfStringEncodingUTF8))
}

func (builder *dictionaryBuilder) addData(key cfRef, value []byte) {
	builder.addOwned(key, builder.api.cfDataCreate(0, unsafe.SliceData(value), len(value)))
}

func (builder *dictionaryBuilder) addArray(key cfRef, elements ...cfRef) {
	builder.addOwned(key, builder.api.cfArrayCreate(0, unsafe.SliceData(elements), len(elements), builder.api.cfTypeArrayCallBacks))
}

func (builder *dictionaryBuilder) build() cfRef {
	dictionary := builder.api.cfDictionaryCreate(0, unsafe.SliceData(builder.keys), unsafe.SliceData(builder.values), len(builder.keys),
		builder.api.cfTypeDictionaryKeyCallBacks, builder.api.cfTypeDictionaryValueCallBacks)
	for _, ownedValue := range builder.owned {
		builder.api.cfRelease(ownedValue)
	}
	builder.owned = nil
	return dictionary
}

func (api *securityAPI) checkStatus(status int32) error {
	return errorFromStatus(status, api.errorMessage)
}

func (api *securityAPI) errorMessage(status int32) string {
	message := api.secCopyErrorMessageString(status, 0)
	if message == 0 {
		return ""
	}
	defer api.cfRelease(message)
	buffer := make([]byte, errorMessageBufferSize)
	if !api.cfStringGetCString(message, unsafe.SliceData(buffer), len(buffer), cfStringEncodingUTF8) {
		return ""
	}
	return strings.TrimRight(string(buffer), "\x00")
}

func (api *securityAPI) copyDataBytes(data cfRef) []byte {
	return append([]byte(nil), unsafe.Slice(api.cfDataGetBytePtr(data), api.cfDataGetLength(data))...)
}

type keychainStore struct {
	path    string
	service string
}

func New(path, service string) secret.Store {
	return keychainStore{path: path, service: service}
}

func Supported() bool {
	return true
}

func (secretStore keychainStore) Get(secretName string) ([]byte, error) {
	if err := validateName(secretName); err != nil {
		return nil, err
	}
	var value []byte
	err := secretStore.withKeychain(secretName, func(api *securityAPI, keychainRef cfRef) error {
		var copyErr error
		value, copyErr = secretStore.copyItemValue(api, keychainRef, secretName)
		return copyErr
	})
	return value, err
}

func (secretStore keychainStore) Set(secretName string, value []byte) error {
	if err := validateName(secretName); err != nil {
		return err
	}
	return secretStore.withKeychain(secretName, func(api *securityAPI, keychainRef cfRef) error {
		status := secretStore.addItem(api, keychainRef, secretName, value)
		if status == errSecDuplicateItem {
			return secretStore.updateItemValue(api, keychainRef, secretName, value)
		}
		return api.checkStatus(status)
	})
}

func (secretStore keychainStore) Delete(secretName string) error {
	if err := validateName(secretName); err != nil {
		return err
	}
	return secretStore.withKeychain(secretName, func(api *securityAPI, keychainRef cfRef) error {
		queryDictionary := secretStore.newItemQueryBuilder(api, keychainRef, secretName).build()
		defer api.cfRelease(queryDictionary)
		return api.checkStatus(api.secItemDelete(queryDictionary))
	})
}

func (secretStore keychainStore) withKeychain(secretName string, operation func(api *securityAPI, keychainRef cfRef) error) error {
	return withOpenKeychain(secretStore.path, func(api *securityAPI, keychainRef cfRef) error {
		if err := operation(api, keychainRef); err != nil {
			return fmt.Errorf("%w: %s in %s", err, secretName, secretStore.path)
		}
		return nil
	})
}

func (secretStore keychainStore) copyItemValue(api *securityAPI, keychainRef cfRef, secretName string) ([]byte, error) {
	queryBuilder := secretStore.newItemQueryBuilder(api, keychainRef, secretName)
	queryBuilder.add(api.secReturnData, api.cfBooleanTrue)
	queryBuilder.add(api.secMatchLimit, api.secMatchLimitOne)
	queryDictionary := queryBuilder.build()
	defer api.cfRelease(queryDictionary)
	var itemData cfRef
	if err := api.checkStatus(api.secItemCopyMatching(queryDictionary, &itemData)); err != nil {
		return nil, err
	}
	defer api.cfRelease(itemData)
	return api.copyDataBytes(itemData), nil
}

func (secretStore keychainStore) addItem(api *securityAPI, keychainRef cfRef, secretName string, value []byte) int32 {
	itemBuilder := secretStore.newItemBuilder(api, secretName)
	itemBuilder.add(api.secUseKeychain, keychainRef)
	itemBuilder.addString(api.secAttrLabel, secretStore.service+": "+secretName)
	itemBuilder.addData(api.secValueData, value)
	itemDictionary := itemBuilder.build()
	defer api.cfRelease(itemDictionary)
	return api.secItemAdd(itemDictionary, nil)
}

func (secretStore keychainStore) updateItemValue(api *securityAPI, keychainRef cfRef, secretName string, value []byte) error {
	queryDictionary := secretStore.newItemQueryBuilder(api, keychainRef, secretName).build()
	defer api.cfRelease(queryDictionary)
	updateBuilder := api.newDictionaryBuilder()
	updateBuilder.addData(api.secValueData, value)
	updateDictionary := updateBuilder.build()
	defer api.cfRelease(updateDictionary)
	return api.checkStatus(api.secItemUpdate(queryDictionary, updateDictionary))
}

func (secretStore keychainStore) newItemBuilder(api *securityAPI, secretName string) *dictionaryBuilder {
	itemBuilder := api.newDictionaryBuilder()
	itemBuilder.add(api.secClass, api.secClassGenericPassword)
	itemBuilder.addString(api.secAttrService, secretStore.service)
	itemBuilder.addString(api.secAttrAccount, secretName)
	return itemBuilder
}

func (secretStore keychainStore) newItemQueryBuilder(api *securityAPI, keychainRef cfRef, secretName string) *dictionaryBuilder {
	queryBuilder := secretStore.newItemBuilder(api, secretName)
	queryBuilder.addArray(api.secMatchSearchList, keychainRef)
	return queryBuilder
}

func withOpenKeychain(path string, operation func(api *securityAPI, keychainRef cfRef) error) error {
	api, err := loadSecurityAPI()
	if err != nil {
		return err
	}
	keychainRef, err := api.openExistingKeychain(path)
	if err != nil {
		return err
	}
	defer api.cfRelease(keychainRef)
	return operation(api, keychainRef)
}

func (api *securityAPI) openExistingKeychain(path string) (cfRef, error) {
	var keychainRef cfRef
	if err := api.checkStatus(api.secKeychainOpen(path, &keychainRef)); err != nil {
		return 0, fmt.Errorf("%w: %s", err, path)
	}
	if err := api.ensureKeychainExists(keychainRef); err != nil {
		api.cfRelease(keychainRef)
		return 0, fmt.Errorf("%w: %s", err, path)
	}
	return keychainRef, nil
}

func (api *securityAPI) ensureKeychainExists(keychainRef cfRef) error {
	var ignoredKeychainStatus uint32
	return api.checkStatus(api.secKeychainGetStatus(keychainRef, &ignoredKeychainStatus))
}

func Create(path string) error {
	return create(path, nil)
}

func createWithPassword(path, password string) error {
	return create(path, []byte(password))
}

func create(path string, password []byte) error {
	api, err := loadSecurityAPI()
	if err != nil {
		return err
	}
	return api.preservingSearchList(func() error {
		return api.createKeychainWithoutAutoLock(path, password)
	})
}

func (api *securityAPI) createKeychainWithoutAutoLock(path string, password []byte) error {
	var keychainRef cfRef
	promptUser := password == nil
	status := api.secKeychainCreate(path, uint32(len(password)), unsafe.SliceData(password), promptUser, 0, &keychainRef)
	if err := api.checkStatus(status); err != nil {
		return fmt.Errorf("create keychain %s: %w", path, err)
	}
	defer api.cfRelease(keychainRef)
	settings := keychainSettings{version: keychainSettingsVersion, lockOnSleep: false, useLockInterval: false, lockInterval: noLockInterval}
	if err := api.checkStatus(api.secKeychainSetSettings(keychainRef, &settings)); err != nil {
		return fmt.Errorf("disable keychain auto-lock %s: %w", path, err)
	}
	return nil
}

func (api *securityAPI) preservingSearchList(operation func() error) (err error) {
	searchListBefore, err := api.copySearchList()
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, api.restoreSearchList(searchListBefore))
		api.cfRelease(searchListBefore)
	}()
	return operation()
}

func (api *securityAPI) restoreSearchList(searchListBefore cfRef) error {
	searchListAfter, err := api.copySearchList()
	if err != nil {
		return err
	}
	defer api.cfRelease(searchListAfter)
	if api.cfArrayGetCount(searchListAfter) == api.cfArrayGetCount(searchListBefore) {
		return nil
	}
	if err := api.checkStatus(api.secKeychainSetSearchList(searchListBefore)); err != nil {
		return fmt.Errorf("restore keychain search list: %w", err)
	}
	return nil
}

func (api *securityAPI) copySearchList() (cfRef, error) {
	var searchList cfRef
	if err := api.checkStatus(api.secKeychainCopySearchList(&searchList)); err != nil {
		return 0, fmt.Errorf("copy keychain search list: %w", err)
	}
	return searchList, nil
}

func setUserInteractionAllowed(allowed bool) error {
	api, err := loadSecurityAPI()
	if err != nil {
		return err
	}
	return api.checkStatus(api.secKeychainSetUserInteractionAllowed(allowed))
}

func searchListCount() (int, error) {
	api, err := loadSecurityAPI()
	if err != nil {
		return 0, err
	}
	searchList, err := api.copySearchList()
	if err != nil {
		return 0, err
	}
	defer api.cfRelease(searchList)
	return api.cfArrayGetCount(searchList), nil
}

func lockKeychain(path string) error {
	return withOpenKeychain(path, func(api *securityAPI, keychainRef cfRef) error {
		return api.checkStatus(api.secKeychainLock(keychainRef))
	})
}
