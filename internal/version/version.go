package version

// Version 由发布流水线通过 -ldflags "-X suse-checkin/internal/version.Version=vX.Y.Z" 注入，
// 本地未注入时保持 dev，便于区分随手编译的二进制。
var Version = "dev"
