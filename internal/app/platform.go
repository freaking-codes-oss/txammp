package app

import "github.com/freaking-codes-oss/txammp/internal/platform"

func currentPlatform() string { return platform.Key() }
