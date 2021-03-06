package config

const (
	UnixRange   = 20
	TokenExpSec = 60
)

const (
	SERVICE_STATUS_DEFAULT        = 0
	SERVICE_STATUS_NO_BENEFICIARY = 1 << 0
	SERVICE_STATUS_NO_WALLET_FILE = 1 << 1
	SERVICE_STATUS_NO_PASSWORD    = 1 << 2
	SERVICE_STATUS_CREATE_ID      = 1 << 3
	SERVICE_STATUS_RUNNING        = 1 << 4
)

var (
	IsNodeInit         = false
	IsWalletInit       = false
	IsIdInit           = false
	Status             = SERVICE_STATUS_DEFAULT
	Token              = ""
	TokenExp     int64 = 0
)
