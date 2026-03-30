ANDROID_NDK ?= /opt/android-sdk/ndk/29.0.14206865
NDK_TOOLCHAIN = $(ANDROID_NDK)/toolchains/llvm/prebuilt/linux-x86_64

# Output directory for built libraries
OUT_DIR ?= build

# Mobile app jniLibs directory
MOBILE_DIR ?= ../flacidal-mobile/android/app/src/main/jniLibs

.PHONY: all android ios linux clean install-android install-ios

all: android

# Build for all Android ABIs
android: android-arm64 android-arm android-x86_64

android-arm64:
	@mkdir -p $(OUT_DIR)/android/arm64-v8a
	CGO_ENABLED=1 \
	GOOS=android \
	GOARCH=arm64 \
	CC=$(NDK_TOOLCHAIN)/bin/aarch64-linux-android21-clang \
	go build -buildmode=c-shared \
		-o $(OUT_DIR)/android/arm64-v8a/libflacidal.so \
		./cmd/bridge/

android-arm:
	@mkdir -p $(OUT_DIR)/android/armeabi-v7a
	CGO_ENABLED=1 \
	GOOS=android \
	GOARCH=arm \
	GOARM=7 \
	CC=$(NDK_TOOLCHAIN)/bin/armv7a-linux-androideabi21-clang \
	go build -buildmode=c-shared \
		-o $(OUT_DIR)/android/armeabi-v7a/libflacidal.so \
		./cmd/bridge/

android-x86_64:
	@mkdir -p $(OUT_DIR)/android/x86_64
	CGO_ENABLED=1 \
	GOOS=android \
	GOARCH=amd64 \
	CC=$(NDK_TOOLCHAIN)/bin/x86_64-linux-android21-clang \
	go build -buildmode=c-shared \
		-o $(OUT_DIR)/android/x86_64/libflacidal.so \
		./cmd/bridge/

# Install .so files into Flutter project jniLibs
install-android: android
	@mkdir -p $(MOBILE_DIR)/arm64-v8a $(MOBILE_DIR)/armeabi-v7a $(MOBILE_DIR)/x86_64
	cp $(OUT_DIR)/android/arm64-v8a/libflacidal.so $(MOBILE_DIR)/arm64-v8a/
	cp $(OUT_DIR)/android/armeabi-v7a/libflacidal.so $(MOBILE_DIR)/armeabi-v7a/
	cp $(OUT_DIR)/android/x86_64/libflacidal.so $(MOBILE_DIR)/x86_64/
	@echo "Installed .so files to $(MOBILE_DIR)"

# Build for host Linux (for testing)
linux:
	@mkdir -p $(OUT_DIR)/linux
	go build -buildmode=c-shared \
		-o $(OUT_DIR)/linux/libflacidal.so \
		./cmd/bridge/

# Build for iOS (requires macOS + Xcode Command Line Tools)
# On macOS: make ios
# SDK path auto-detected via xcrun; override with IOS_SDK_PATH if needed
IOS_SDK_PATH ?= $(shell xcrun --sdk iphoneos --show-sdk-path 2>/dev/null)
IOS_CLANG ?= $(shell xcrun --sdk iphoneos --find clang 2>/dev/null || echo clang)
IOS_MIN_VERSION ?= 16.0

ios:
	@mkdir -p $(OUT_DIR)/ios
	@if [ -z "$(IOS_SDK_PATH)" ]; then \
		echo "ERROR: iOS SDK not found. Xcode + Command Line Tools required."; \
		echo "  Install: xcode-select --install"; \
		echo "  Or set IOS_SDK_PATH manually."; \
		exit 1; \
	fi
	CGO_ENABLED=1 \
	GOOS=ios \
	GOARCH=arm64 \
	CC="$(IOS_CLANG) -arch arm64 -isysroot $(IOS_SDK_PATH) -miphoneos-version-min=$(IOS_MIN_VERSION)" \
	go build -buildmode=c-archive \
		-o $(OUT_DIR)/ios/libflacidal.a \
		./cmd/bridge/

# Install .a + header into Flutter iOS project
IOS_MOBILE_DIR ?= ../flacidal-mobile/ios/Runner
install-ios: ios
	@mkdir -p $(IOS_MOBILE_DIR)
	cp $(OUT_DIR)/ios/libflacidal.a $(IOS_MOBILE_DIR)/
	cp $(OUT_DIR)/ios/libflacidal.h $(IOS_MOBILE_DIR)/
	@echo "Installed libflacidal.a + .h to $(IOS_MOBILE_DIR)"

clean:
	rm -rf $(OUT_DIR)
