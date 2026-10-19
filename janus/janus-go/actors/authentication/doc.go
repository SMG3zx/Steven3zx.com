// Package authentication provides password hashing and JWT issuance and
// validation for Janus accounts. Its Service holds only configuration, so
// callers use it directly and concurrent sign-ins never queue behind each other.
package authentication
