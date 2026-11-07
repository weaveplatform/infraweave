// Package private keeps a file to its owner alone, which a file mode
// cannot do on every OS: on Windows os.Chmod sets only the read-only
// attribute, so a file created 0600 inherits its directory's ACL. Restrict
// sets mode 0600, or on Windows a protected DACL that allows the process's
// user alone; Check fails unless a file is restricted so, which tests use
// to check a secret's file on each OS rather than skip Windows.
//
// # Input and output
//
// It changes and reads the permissions of the files it is given.
//
// # Dependencies
//
// It imports the standard library and, on Windows,
// golang.org/x/sys/windows. infraweave secrets generate-kek and
// infraweavectl's profiles file use it.
package private
