# virt-gtk

virt-manager is a reasonably nice GUI for *creating* VM's. However, for actually *running* them, it leaves a lot to be desired:

* Lots of input and video lag.
* Even worse lag when running cross-endian, e.g. a ppc64 guest on a ppc64le host.
* Even worse lag when many VM's are running simultaneously.
* ~~No audio.~~ This seems to have eventually been fixed.
* No searching for VM's by name substring.

Much of this shittiness seems to be due to Red Hat engineers smoking crack and deciding that "let's pipe the entire VM's framebuffer through a SPICE Unix socket instead of just using shared memory" was a good idea. Additional shittiness seems to be due to Red Hat engineers smoking LSD and deciding that "let's run all VM's on the same thread, so all your active VM's will hang for a couple seconds when one VM is booting" was a good idea. Things don't look good on the horizon, due to Red Hat engineers smoking krokodil and deciding "the real problem with virt-manager is that it's not laggy enough, let's rewrite the whole thing to run inside a web browser." (I've been told that there were probably other drugs and corresponding bad decisions involved in the shittiness too.)

And that's where virt-gtk comes in. With virt-gtk, you can run an existing VM (created in virt-manager) using standard QEMU with the standard GTK GUI (no SPICE). It's much less laggy than virt-manager, audio works fine, and VM's show up in your desktop launcher where you can easily search for them.

## But what's the catch?

virt-gtk runs QEMU with less sandboxing than virt-manager:

* virt-gtk runs QEMU under the `audio` and `netdev` groups.
* virt-gtk grants QEMU access to the PulseAudio and Wayland sockets.
* virt-gtk disables QEMU's seccomp sandboxing.

This means that virt-gtk has less security against malicious VM's than virt-manager does. I am admittedly unhappy about this, and I would be excited to receive community contributions that improve sandboxing.

On the other hand, if virt-manager's security comes at the cost of making VM's so painful to use that you wind up doing some things without VM's at all, then virt-gtk is probably better security for you. Unusable security isn't real security.

## Installation

```
pushd cmd/virt-gtk
go build
sudo cp -a ./virt-gtk /usr/sbin/
popd

push cmd/virt-gtk-update
go build
sudo cp -a ./virt-gtk-update /usr/sbin/
popd

sudo adduser libvirt-qemu audio
sudo adduser libvirt-qemu netdev

# DO NOT enable the setuid attribute on qemu-bridge-helper like the QEMU docs say
# here: https://wiki.qemu.org/Features/HelperNetworking#Setup
# setcap works fine and has less attack surface.
sudo setcap 'cap_net_admin+eip' /usr/lib/qemu/qemu-bridge-helper

mkdir -p ~/.config/pipewire/pipewire-pulse.conf.d/
nano ~/.config/pipewire/pipewire-pulse.conf.d/pulse-server.conf

# Paste the following:
pulse.properties = {
    server.address = [
        "unix:native"
        "unix:/tmp/pulse-socket"
    ]
}

systemctl --user restart pipewire
```

## Instructions

First, create your VM's in virt-manager, just like you would normally.

Then, run the following:

```
sudo virt-gtk-update
```

This will create an application in your desktop launcher for each VM that you currently have. You'll need to redo this step whenever you create, delete, or rename a VM.

To run a VM, just choose it in your desktop launcher (it'll be in the `System` category). You can search for VM's in your desktop launcher just like you would search for other applications.

What, were you hoping it would be more complicated than that?

### Installing an OS

If you would like to run the OS installer inside virt-gtk, do the following:

1. Create the VM in virt-manager as normal, choosing the installation media image inside virt-manager.
2. As soon as virt-manager launches the VM and the installer shows the GRUB boot menu, force-power-off the VM.
3. Go to virt-manager settings, and re-select the installation media image for the CDROM device.
4. Re-enable the CDROM device in the Boot Options list.
5. Run `sudo virt-gtk-update`.
6. Launch the VM in virt-gtk.
7. Once the installer has finished, you can optionally remove the media image for the CDROM device, disable booting from CDROM, and/or remove the CDROM device completely.

## Credits

Copyright (C) 2024-2026 Jeremy Rand.

virt-gtk is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

virt-gtk is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with virt-gtk.  If not, see [https://www.gnu.org/licenses/](https://www.gnu.org/licenses/).

Greetings to JTL, RobbieAB, dormito, A. Wilcox, Hugo Landau, Rose Turing, and Marty Rand for keeping me company during development.
