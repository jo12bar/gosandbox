package main

import (
	"crypto/md5"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// walkFiles starts a goroutine to walk the directory tree at root and send the
// path of each regular file on the string channel. It sends the result of the
// walk on the error channel. If done is closed, walkFiles abandons its work.
func walkFiles(done <-chan struct{}, root string) (<-chan string, <-chan error) {
	paths := make(chan string)
	errc := make(chan error, 1)

	go func() {
		// Close the paths channel after Walk returns.
		defer close(paths)

		// No select needed for this send, since errc is buffered.
		errc <- filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}

			select {
			case paths <- path:
			case <-done:
				return errors.New("walk canceled")
			}

			return nil
		})
	}()

	return paths, errc
}

// result is a MD5 checksum computation result, with an optional error.
type result struct {
	path string
	sum  [md5.Size]byte
	err  error
}

// digester reads path names from paths and sends digests of the corresponding
// files on c until either paths or done is closed.
func digester(done <-chan struct{}, paths <-chan string, c chan<- result) {
	for path := range paths {
		data, err := ioutil.ReadFile(path)

		select {
		case c <- result{path, md5.Sum(data), err}:
		case <-done:
			return
		}
	}
}

// MD5All reads all the files in the file tree rooted at root and returns a map
// from file path to the MD5 sum of the file's contents. If the directory walk
// fails or any read operation fails, MD5All returns an error. In that case,
// MD5All does not wait for inflight read operations to complete.
func MD5All(root string) (map[string][md5.Size]byte, error) {
	// MD5All closes the done channel when it returns; it may do so before
	// receiving all the values from c and errc.
	done := make(chan struct{})
	defer close(done)

	paths, errc := walkFiles(done, root)

	// Start a fixed number of goroutines to read and digest files.
	c := make(chan result)
	var wg sync.WaitGroup
	const numDigesters = 20

	wg.Add(numDigesters)

	for i := 0; i < numDigesters; i++ {
		go func() {
			digester(done, paths, c)
			wg.Done()
		}()
	}

	go func() {
		wg.Wait()
		close(c)
	}()

	// Collect the results from c.
	m := make(map[string][md5.Size]byte)
	for r := range c {
		if r.err != nil {
			return nil, r.err
		}
		m[r.path] = r.sum
	}

	// Check whether the walk failed.
	if err := <-errc; err != nil {
		return nil, err
	}

	return m, nil
}

func main() {
	// Calculate the MD5 sum of all files under the specified directory,
	// then print all the results sorted by name.
	m, err := MD5All(os.Args[1])

	if err != nil {
		fmt.Println(err)
		return
	}

	var paths []string
	for path := range m {
		paths = append(paths, path)
	}

	sort.Strings(paths)

	for _, path := range paths {
		fmt.Printf("%x\t%s\n", m[path], path)
	}
}
