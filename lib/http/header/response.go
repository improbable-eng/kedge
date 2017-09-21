package header

const (
	// ResponseKedgeError header is used to expose in HTTP response real error why request was not proxied.
	ResponseKedgeError = "X-Kedge-Error"

	// ResponseKedgeError header is used to expose in HTTP respone error Type of the error resulting in request not proxied.
	ResponseKedgeErrorType ="X-Kedge-Error-Type"
)