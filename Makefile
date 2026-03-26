ANDROID_NDK ?= /opt/android-sdk/ndk/29.0.14206865
NDK_TOOLCHAIN = $(ANDROID_NDK)/toolchains/llvm/prebuilt/linux-x86_64

# Output directory for built libraries
OUT_DIR ?= build

# Mobile app jniLibs directory
MOBILE_DIR ?= ../flacidal-mobile/android/app/src/main/jniLibs

.PHONY: all android ios linux clean install-android

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

# Build for iOS (requires macOS + Xcode)
ios:
	@mkdir -p $(OUT_DIR)/ios
	CGO_ENABLED=1 \
	GOOS=ios \
	GOARCH=arm64 \
	go build -buildmode=c-archive \
		-o $(OUT_DIR)/ios/libflacidal.a \
		./cmd/bridge/

clean:
	rm -rf $(OUT_DIR)
