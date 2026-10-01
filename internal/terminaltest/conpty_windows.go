package terminaltest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ConPTYProcess keeps native Console input/size/modes in the child while its
// parent exchanges VT bytes over pipes. It is separate from the Unix PTY API.
type ConPTYProcess struct {
	input     *os.File
	output    *os.File
	console   windows.Handle
	process   windows.Handle
	done      chan struct{}
	readDone  chan struct{}
	mu        sync.Mutex
	text      bytes.Buffer
	waitErr   error
	closeOnce sync.Once
}

// StartConPTY starts a controlled child without opening a visible window.
// Output is drained continuously so ClosePseudoConsole cannot block on a pipe.
func StartConPTY(command *exec.Cmd, width, height uint16) (_ *ConPTYProcess, resultErr error) {
	if command == nil || width == 0 || height == 0 || width > 32767 || height > 32767 {
		return nil, errors.New("ConPTY command and valid size are required")
	}
	var inputRead, inputWrite, outputRead, outputWrite windows.Handle
	if err := windows.CreatePipe(&inputRead, &inputWrite, nil, 0); err != nil {
		return nil, err
	}
	defer func() {
		if inputRead != 0 {
			windows.CloseHandle(inputRead)
		}
	}()
	if err := windows.CreatePipe(&outputRead, &outputWrite, nil, 0); err != nil {
		windows.CloseHandle(inputWrite)
		return nil, err
	}
	defer func() {
		if outputWrite != 0 {
			windows.CloseHandle(outputWrite)
		}
	}()
	process := &ConPTYProcess{
		input:  os.NewFile(uintptr(inputWrite), "conpty-input"),
		output: os.NewFile(uintptr(outputRead), "conpty-output"),
		done:   make(chan struct{}), readDone: make(chan struct{}),
	}
	defer func() {
		if resultErr != nil {
			process.Close()
		}
	}()
	if err := windows.CreatePseudoConsole(windows.Coord{X: int16(width), Y: int16(height)}, inputRead, outputWrite, 0, &process.console); err != nil {
		return nil, err
	}
	windows.CloseHandle(inputRead)
	windows.CloseHandle(outputWrite)
	inputRead, outputWrite = 0, 0
	go func() {
		defer close(process.readDone)
		var buffer [4096]byte
		for {
			n, err := process.output.Read(buffer[:])
			if n > 0 {
				process.mu.Lock()
				process.text.Write(buffer[:n])
				process.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return nil, err
	}
	defer attributes.Delete()
	// This attribute takes the HPCON value itself, unlike attributes that take
	// an address. Pass it as a syscall argument without inventing a Go pointer.
	update := windows.NewLazySystemDLL("kernel32.dll").NewProc("UpdateProcThreadAttribute")
	updated, _, updateErr := update.Call(uintptr(unsafe.Pointer(attributes.List())), 0,
		windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, uintptr(process.console), unsafe.Sizeof(process.console), 0, 0)
	if updated == 0 {
		return nil, fmt.Errorf("attach ConPTY: %w", updateErr)
	}
	startup := windows.StartupInfoEx{ProcThreadAttributeList: attributes.List()}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	startup.Flags = windows.STARTF_USESTDHANDLES
	application, err := windows.UTF16PtrFromString(command.Path)
	if err != nil {
		return nil, err
	}
	arguments, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(command.Args))
	if err != nil {
		return nil, err
	}
	var directory *uint16
	if command.Dir != "" {
		directory, err = windows.UTF16PtrFromString(command.Dir)
		if err != nil {
			return nil, err
		}
	}
	environment := command.Env
	if environment == nil {
		environment = os.Environ()
	}
	environment = append([]string{}, environment...)
	sort.Slice(environment, func(i, j int) bool { return strings.ToUpper(environment[i]) < strings.ToUpper(environment[j]) })
	for _, entry := range environment {
		if strings.ContainsRune(entry, 0) {
			return nil, errors.New("NUL in ConPTY environment")
		}
	}
	block := utf16.Encode([]rune(strings.Join(environment, "\x00") + "\x00\x00"))
	var information windows.ProcessInformation
	if err := windows.CreateProcess(application, arguments, nil, nil, false,
		windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT,
		&block[0], directory, &startup.StartupInfo, &information); err != nil {
		return nil, err
	}
	process.process = information.Process
	windows.CloseHandle(information.Thread)
	go func() {
		defer close(process.done)
		_, err := windows.WaitForSingleObject(process.process, windows.INFINITE)
		if err == nil {
			var code uint32
			err = windows.GetExitCodeProcess(process.process, &code)
			if err == nil && code != 0 {
				err = fmt.Errorf("ConPTY child exited with status %d", code)
			}
		}
		process.waitErr = err
	}()
	return process, nil
}

// Input returns the parent-to-child input pipe.
func (process *ConPTYProcess) Input() *os.File { return process.input }

// Output snapshots all VT bytes received so far.
func (process *ConPTYProcess) Output() string {
	process.mu.Lock()
	defer process.mu.Unlock()
	return process.text.String()
}

// Resize changes the native console size exposed to the child.
func (process *ConPTYProcess) Resize(width, height uint16) error {
	if width == 0 || height == 0 || width > 32767 || height > 32767 {
		return errors.New("invalid ConPTY size")
	}
	return windows.ResizePseudoConsole(process.console, windows.Coord{X: int16(width), Y: int16(height)})
}

// Wait bounds waiting for the child, leaving cleanup to Close on timeout.
func (process *ConPTYProcess) Wait(ctx context.Context) error {
	select {
	case <-process.done:
		return process.waitErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close terminates an unfinished child and releases native handles and pipes.
func (process *ConPTYProcess) Close() error {
	process.closeOnce.Do(func() {
		if process.process != 0 {
			select {
			case <-process.done:
			default:
				_ = windows.TerminateProcess(process.process, 1)
				<-process.done
			}
			windows.CloseHandle(process.process)
		}
		process.input.Close()
		if process.console != 0 {
			windows.ClosePseudoConsole(process.console)
			<-process.readDone
		}
		process.output.Close()
	})
	return nil
}
