// pkg/verify/options.go
package verify

// Options configures the verify operation
type Options struct {
	// InputPath is the archive file to verify (required)
	InputPath string

	// VerifyData performs full data integrity check by decompressing all data
	// When false, only structural validation is performed (faster)
	// Default: false
	VerifyData bool

	// Verbose enables detailed logging during verification
	Verbose bool

	// Quiet suppresses all output except errors
	Quiet bool
}

// Validate checks if options are valid
func (o *Options) Validate() error {
	if o.InputPath == "" {
		return ErrInputRequired
	}
	if o.Quiet {
		o.Verbose = false
	}
	return nil
}
