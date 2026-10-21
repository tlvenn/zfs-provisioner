package idmap

import (
	"fmt"
	"os"
	"runtime"

	"golang.org/x/sys/unix"
)

func swap(pid int, hostPath, ctrPath string, replace bool) error {
	userns, err := os.Open(fmt.Sprintf("/proc/%d/ns/user", pid))
	if err != nil {
		return err
	}
	defer userns.Close()

	mntns, err := os.Open(fmt.Sprintf("/proc/%d/ns/mnt", pid))
	if err != nil {
		return err
	}
	defer mntns.Close()

	// Detached clone of the host mount and everything beneath it, idmapped
	// through the container's user namespace like Incus' shift=true mounts.
	tree, err := unix.OpenTree(unix.AT_FDCWD, hostPath, unix.OPEN_TREE_CLONE|unix.OPEN_TREE_CLOEXEC|unix.AT_RECURSIVE)
	if err != nil {
		return fmt.Errorf("open_tree %s: %w", hostPath, err)
	}
	defer unix.Close(tree)

	// A clone of a shared mount joins the original's peer group, so mount
	// events would flow both ways: stopping the container would unmount the
	// host's nested datasets. As a slave it only receives the host's events,
	// like the mounts Incus makes itself.
	attr := unix.MountAttr{
		Attr_set:    unix.MOUNT_ATTR_IDMAP,
		Userns_fd:   uint64(userns.Fd()),
		Propagation: unix.MS_SLAVE,
	}
	if err := unix.MountSetattr(tree, "", unix.AT_EMPTY_PATH|unix.AT_RECURSIVE, &attr); err != nil {
		return fmt.Errorf("mount_setattr idmap: %w", err)
	}

	errCh := make(chan error, 1)
	go func() {
		// setns(CLONE_NEWNS) is refused while the thread shares filesystem
		// attributes with the rest of the process, and the thread is no use
		// to the host afterwards: it stays locked so it exits with this
		// goroutine.
		runtime.LockOSThread()

		if err := unix.Unshare(unix.CLONE_FS); err != nil {
			errCh <- fmt.Errorf("unshare: %w", err)
			return
		}
		if err := unix.Setns(int(mntns.Fd()), unix.CLONE_NEWNS); err != nil {
			errCh <- fmt.Errorf("setns: %w", err)
			return
		}
		if replace {
			if err := unix.Unmount(ctrPath, unix.MNT_DETACH); err != nil {
				errCh <- fmt.Errorf("umount %s: %w", ctrPath, err)
				return
			}
		}
		if err := unix.MoveMount(tree, "", unix.AT_FDCWD, ctrPath, unix.MOVE_MOUNT_F_EMPTY_PATH); err != nil {
			errCh <- fmt.Errorf("move_mount to %s: %w", ctrPath, err)
			return
		}
		// Shared within the container on top of slave to the host, matching
		// Incus' device mounts so binds made inside the container behave
		// the same on either kind.
		shared := unix.MountAttr{Propagation: unix.MS_SHARED}
		if err := unix.MountSetattr(unix.AT_FDCWD, ctrPath, unix.AT_RECURSIVE, &shared); err != nil {
			errCh <- fmt.Errorf("mount_setattr shared %s: %w", ctrPath, err)
			return
		}
		errCh <- nil
	}()

	return <-errCh
}
