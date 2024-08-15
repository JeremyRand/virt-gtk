# virt-gtk

virt-manager is a reasonably nice GUI for *creating* VM's. However, for actually *running* them, it leaves a lot to be desired:

* Lots of input and video lag.
* Even worse lag when running cross-endian, e.g. a ppc64 guest on a ppc64le host.
* No audio.
* No searching for VM's by name substring.

Much of this shittiness seems to be due to Red Hat engineers smoking crack and deciding that "let's pipe the entire VM's framebuffer through a SPICE Unix socket instead of just using shared memory" was a good idea. (I've been told that there were probably other drugs and corresponding bad decisions involved in the shittiness too.)

And that's where virt-gtk comes in. With virt-gtk, you can run an existing VM (created in virt-manager) using standard QEMU with the standard GTK GUI (no SPICE). It's much less laggy than virt-manager, audio works fine, and you can search for VM's with whatever search functionality your file manager (e.g. Dolphin) has.

## But what's the catch?

virt-gtk runs QEMU as root, and without sandboxing. This means that it has less security against malicious VM's than virt-manager does. I am admittedly unhappy about this, and I would be excited to receive community contributions that fix this.

On the other hand, if virt-manager's security comes at the cost of making VM's so painful to use that you wind up doing some things without VM's at all, then virt-gtk is probably better security for you. Unusable security isn't real security.

## Instructions

First, run the following:

```
sudo ./virt-gtk-update
```

This will create a launcher for each VM that you currently have. You'll need to redo this whenever you create, delete, or rename a VM.

Then, to run a VM, go to the `launchers` folder that was created, and run the `.desktop` file corresponding to the VM you want. If you have a lot of launchers and want to search by name substring, just use your file manager (e.g. Dolphin) for that.

What, were you hoping it would be more complicated than that?

## Credits

Copyright (C) 2024 Jeremy Rand.

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
