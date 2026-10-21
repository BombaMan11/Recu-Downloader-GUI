# Recurbate (window version)

The same HLS downloader as the command-line program, with a download-manager window
(like qBittorrent or JDownloader): a table of links, start/pause per row, queue order,
three download modes, and settings that are remembered.

## Using it

1. Run `Recu.exe`. A window opens (Edge or Chrome app window).
2. Open **Settings** (gear, top right) and fill in **User-Agent** and **Cookie**. Nothing
   downloads until both are set. They are saved in `settings.json` and are still there
   next time.
   When the site gives you a new cookie, copy it in your browser and press the key button
   (**Replace cookie**) at the top right of the main window, next to the clipboard button. It
   asks you to confirm, then saves what you copied as the cookie, nothing else is changed.
   Downloads that are running or waiting are never left on the old cookie: they are paused
   first, as if you pressed **Pause all**, and the cookie is replaced. A message says
   "Downloads Paused to Replace Cookies". Press **Start** (or **Start all**) when you want
   them to carry on with the new cookie. If you answer No to the question, nothing changes.
   Saving a different cookie in Settings asks the same question and does the same.
   The **Replace cookie** button in Settings (next to the box) does the same with what you
   copied. The program reads the clipboard itself, so the browser never asks for permission.
   If there is no text on the clipboard, nothing is replaced.
3. Paste links in the box at the top, one per line, and press **Add to list**:

   ```
   https://Recu.me/video/1234567/play
   ["https://Recu.me/video/1234567/play","55:00","1:10:00"]
   ["https://Recu.me/video/1234567/play","55:00","1:10:00","1:30:00"]
   ```

   The second form downloads only 55:00 to 1:10:00 of the video; its length is read from the
   video's playlist. The third form states the length of the whole video yourself (it is
   used as typed). The same link with another start/end is a separate row; the same part
   written another way (`55:00` / `00:55:00`) counts as a duplicate.
   `Find links on a page…` collects video links from a page.

## The table

| Column | Meaning |
| --- | --- |
| checkbox | **Active.** An inactive row never starts (not even with Start all). |
| Size / Full size | Size downloaded so far, and the size the finished file will have. While downloading, Full size is an estimate (`~`) worked out from the segments done so far and the part of the video being downloaded; it becomes exact when the file is finished. |
| Start / End / Duration | Which part of the video is downloaded, as `hh:mm:ss`. A whole video starts at `00:00:00`; its End (and so its Duration) is the length of the video, which the program reads from the playlist in the background shortly after you add the link (no download is started), so it shows up a moment later. Duration is End minus Start. |
| Server | Which server the video comes from. Same colour = same server. |
| Server pill | `Next` / `#2`… = the order in which that server's rows will start (Hybrid). |

**Resize columns** by dragging the border on the right of a heading. The widths are saved in
`settings.json`, so they are kept after the program is closed. Settings → Table → *Reset column
widths* goes back to the default layout (it asks first).

**Sort** by clicking a heading (or right-click a heading → *Sort by Name*, *Sort by Name (Z to A)*,
*Back to the queue order*): text sorts A to Z, sizes, speeds and times sort largest or longest
first. Click again to reverse, a third time (or click `#`) to go back to the queue order. On its
own, sorting only changes what is shown; the `#` column keeps showing each row's place in the
queue.

**Sort the real queue.** While a column is sorted, the move buttons (and the right-click menu)
use that sort to put the selected rows in order in the queue itself:

* **Move to top**: the selected rows go to the top of the queue, in the order shown (for Name
  sorted A to Z: A to Z).
* **Move to bottom**: the selected rows go to the bottom, in the opposite order (Z to A).
* **Move up / Move down**: the whole list takes the order shown, then the selected rows move
  one step.

So to sort the whole queue by name A to Z: sort by Name, Ctrl+A, **Move to top**. For Z to A,
**Move to bottom** instead. Rows that you don't select keep their places. Afterwards the sort is
cleared and the table shows the queue as it now is. Other columns work the same way (largest
size, longest duration, ...).

**Drag a box** over the rows to select them, like in Explorer (Ctrl adds or removes the rows
the box touches; near the top or bottom edge the list scrolls along).

**Double-click** a row to show its file in Explorer, selected in the unfinished or finished
folder, wherever it is now (also in the right-click menu as *Show file in folder*).

Select rows with click, Ctrl+click, Shift+click, Ctrl+A. Toolbar and right-click menu:
Start, Pause, **Force start** (ignores the mode), **Force start all**, Start all, Pause all,
move to top / up / down / bottom, Playlist (saves the `.m3u8`), Remove, Clear finished.
**Remove** (toolbar, right-click menu or the Delete key) always asks first. The question has a
box, **Also delete the files from disk**, that is off by default: with it off only the rows leave
the list and the videos stay where they are.

## Keeping your master list safe

The text file the listener writes to can only **gain** lines at the top. Recurbate never
replaces, shortens or deletes what is in it.

- The new file is written next to the old one (new lines + the old content, byte for byte),
  read back and compared, and only then swapped in. If anything goes wrong the old file is
  exactly as it was.
- If another program changed the file in the meantime, nothing is swapped: the file is read
  again and the write retried. Recurbate also takes a `<file>.lock` file while it writes, and
  waits for it if another program that uses the same lock file is writing.
- Once a day, before the first change, the file is copied to `<file>.backups\` (for example
  `All-Stored-links.txt.backups\All-Stored-links.txt.2026-10-18.bak`). A backup is never
  overwritten and Recurbate never deletes one. `added.log` there lists every line added
  with the time, so the file can be rebuilt from the newest `.bak` plus `added.log`.
- A file that is **missing** but has backups is not created again (that would start a new
  file with one line in it and hide that the old one is gone). The listener shows why. Put the
  file back from the newest `.bak`; to start a new file on purpose, move the `.backups`
  folder away.
- The listener's temporary file (`.Recurbate-links-….tmp`) is made in Recurbate's data folder,
  next to `settings.json`, and deleted as soon as the swap is done. The swap is only instant and
  all-or-nothing on one drive, so when the archive is on another drive than the data folder the
  temporary file goes next to the archive instead (move the data folder to the archive's drive in
  Settings if you want them in one place).
- Binary files, folders and non-text file types are refused.

In Settings → Link listener, **Archive** (save the links to the text file) and **Backups** (the
daily copies and `added.log` above) are two tick boxes. Both are off by default on a new
install; settings saved by an older version keep what they had. You can have the archive without backups, but not backups
without the archive: with the archive off, the Backups box is locked and unticked and the
listener leaves the file alone (the link box still gets the links). Safe writing (the points above
except the daily copy) is always on, whatever the boxes say. With backups off, no `.backups`
folder is made, and nothing already in the file is ever removed; turning them off does not delete
copies that already exist.

## Link listener

Like the clipboard observer in JDownloader. Switch it on with the clipboard button at the top
right of the main window (it lights up while it is on; the choice is remembered). It is on
by default on a new install. While it is on, each time you copy something, Recurbate looks for links in it and puts each one on its own
line in the link box on the main window (ready for *Add to list*) and, if the Archive is
ticked, in a text file. The text
file gets new links at the **very top**, above the links already there; the link box gets them
**under the last link**:

```
copy  ["https://Recu.me/video/xxxxxxx/play","55:00","1:10:00","1:30:00"],
file  https://Recu.me/video/xxxxxxx/play
box   ["https://Recu.me/video/xxxxxxx/play","55:00","1:10:00","1:30:00"]

copy  https://Recu.me/video/xxxxxxy/play
file  https://Recu.me/video/xxxxxxy/play        <- new link on top
      https://Recu.me/video/xxxxxxx/play
box   ["https://Recu.me/video/xxxxxxx/play","55:00","1:10:00","1:30:00"]
      https://Recu.me/video/xxxxxxy/play        <- new link at the bottom
```

If what you copied is a whole entry such as
`["https://Recu.me/video/xxxxxxx/play","0:00:00","1:30:00","1:30:00"],` (as the browser script's
*Next* and *Full Link* buttons copy), the box gets that **whole entry**, with its start and end
times (without the trailing comma), while the text file gets only the plain link. A plain link
copied on its own goes into both as it is. Another clip of the same video (different times) is a new
entry in the box; the file keeps the link once. A line that is already in the box is not put
there again. Pressing *Add to list* empties the box as usual, so the next copy starts a fresh list.

Set it up in Settings → **Link listener**:

* **Listen to Clipboard and Add to URL Box** (on by default): the listener itself. Every link it
  finds goes into the link box on the main window.
* **Archive** (off by default) and **Text file** (blank by default): with Archive ticked, the full path of a `.txt` file (also `.text`, `.list`, `.lst`, `.md`, `.csv`). It
  is created if missing, but its folder must exist. The file's other lines, line endings and
  byte order mark are kept.
* **What counts as a link**: one regular expression per line (Go/RE2 syntax). Every match is
  added; if a pattern has a group in brackets, only the group is added, otherwise the whole
  match. To catch another site add a line such as `https://other\.me/v/\d+`, or
  `"(https://other\.me/v/\d+)"` to leave the quotes out. Several links in one copy are added
  in the order they appear in the text. Blank lines and `#` lines are ignored; with nothing
  there the default (Recurbate's own video links) is used.
* **Skip links that are already in the file** (on by default).
* **Also read copies from private windows** (on by default): browsers mark everything copied in a
  private or incognito window as private, and password managers do the same for passwords. The
  listener skips those, and says so with a message (at most every 20 seconds), so if links copied
  by the browser script never arrive and you browse privately, tick this. Only the links the
  patterns find are ever used, whatever else was copied.
* **Try the patterns**: paste some text and press *Test patterns* to see what would go into the link
  box and into the text file, without saving anything.

What was already on the clipboard when you switch it on is not added. If the file can't be
written (folder missing, file open in a program that locks it), a message says so and the links
are kept and tried again until it works. It only runs while Recurbate is open.

**Safety.** It only runs while Recurbate is open, and you can switch it off at any time. It only looks at copied text and keeps nothing but
the links the patterns find; clipboards that the program that made them marked as private (private browser windows, password managers) are skipped unless you allow them.
Patterns can't hang the program (RE2 has no catastrophic backtracking), and their number and
length, the size of the clipboard text (1 MB), the links per copy (500) and the file size (50 MB)
are capped. It only writes to the one text file you chose, never to a program or settings file,
and refuses a file that doesn't look like text. Nothing is sent anywhere. The checks are done
by the program itself; the window only shows the result.

## Categories

Like qBittorrent: Settings → **Categories** lets you create categories, each with its own
folders for unfinished videos, finished videos and playlists (a blank folder uses the normal
folder). Choose a category in the box next to **Add to list**; the **Category** column
(right of Server) shows it. To change it later, right-click a row → *Set category*. That asks
first, because it **deletes the row's files from the old category's folders** (unfinished
video, finished video and saved playlist) and starts the download again from the beginning in
the new folders; a running download is stopped first. Removing a category in Settings only
takes it off its rows, nothing is deleted. Changing a category's folders (or the normal
folders) affects downloads that start afterwards; a download already in progress carries on in
the folder where it began.

## Modes

* **Normal**: every active row at the same time (optional cap in Settings).
* **Hybrid**: one row per server, different servers at the same time, top to bottom.
* **Series**: one row at a time, top to bottom.

**Start all** first looks up the server of every waiting or paused row again (it only reads
the playlists, nothing is downloaded yet), because the site can use a different server the
next time, for example after the program was closed and opened again. Then the rows start in
the order the mode allows. Servers saved from an earlier run are never tRecued.

**Start** (and Force start) does the same for the selected rows only, including a row that was
paused and is simply resumed: its server is looked up again, the other rows are left alone.

Force-started rows ignore these limits and don't count against them. Changing the mode
never stops a download that is already running; the new rule applies to what starts next.

## Leaving it running (403 / Cloudflare)

The links to the video pieces are signed and stop working after a while, and the site's
Cloudflare answers **403** when they have, or when it decides to block the program. The
original program stopped with that error. This one keeps going on its own:

- Shortly **before** the links run out it reads the playlist again and carries on from the
  same piece, so the site never has to turn a request away.
- If a piece is refused anyway (403, 401 or 410), it reads the playlist again and resumes
  from the saved position. This repeats as often as needed: the count starts over each time
  a piece comes through.
- If new links are refused too, the site is blocking the program (or the network is away).
  The row shows "Trying again in …" and asks again after 20 s, 1 min, 3 min and 10 min.
  Replacing the cookie (the key button at the top right) pauses the waiting rows; press Start to try again with the new cookie.
- Only then does the row show an error, in plain words (no page source), saying to open the
  site in your browser, copy the new cookie, press the key button (**Replace cookie**) and press **Start**. It continues from where
  it stopped, nothing is downloaded twice.
- A "too many requests" (429) answer is now asked again with growing pauses instead of every
  0.1 s, which is a quick way to get blocked. Hybrid's server check of waiting rows waits and
  tries again too, instead of failing rows that never started.

If the cookie itself has expired (the site logged you out, or `cf_clearance` ran out) no
amount of waiting helps; that is the case the final message is for.

## Folders and files

By default everything is kept in the folder the program is in, with no subfolders: videos
still downloading, finished videos, playlists saved with the Playlist button, and
`settings.json`, `queue.json` and `Recurbate.log`. A video keeps the same file while it
downloads and when it finishes (it just stops growing).

In Settings every folder box is blank by default (blank = the program's folder). A category
that leaves a folder blank uses the normal folder, so it ends up there too.
Type a full path such as `D:\Videos` or press **Browse…** to use another folder, on any drive.

**Program data folder** (Settings) is one folder for everything the program keeps for
itself: `settings.json`, `queue.json`, `Recurbate.log` and the data of the program's own
window (`browser-data`, a browser profile). Saving a new folder moves settings, queue and
log there at once; the window's data follows the next time the program starts (it is in use
until then). A folder that already holds a `settings.json` or `queue.json` is refused, so
nothing is overwritten. Because `settings.json` can't say where it is itself, the choice is
remembered in `Recurbate.paths.json` next to the program. A new install uses a folder called
`Recu-Settings` next to `Recu.exe` (the file is made on the first start). Delete the file to go
back to that default; leave the box blank and save to keep everything next to `Recu.exe`
instead. A copy of the program that already has a `settings.json` next to it keeps using it.
Nothing is written to AppData.

**Defaults on a new install.** *Start new rows as soon as they're added* and *Continue
unfinished downloads when Recurbate starts* are both off, so nothing downloads until you press
Start. The link listener is on (see Link listener), with the Archive and Backups boxes off, the
text file blank, *Skip links that are already in the file* on and *Also read copies from private
windows* on. Settings saved by an earlier version keep the values they already have.

## Closing

Closing the window quits the program: running downloads are paused and saved, and
continue the next time you start it.

The power button under the gear does the same on demand: it asks first if downloads are
running or waiting, pauses and saves them, then closes the window and the program.

## Command line

`-dir <folder>` use another folder · `-port <n>` fixed port · `-no-window` print the address
instead of opening a window · `-version`.

## Building Recu.exe on a new Windows PC

You need Windows 10 or 11 (64-bit) and an internet connection for step 1. The program has no
other downloads: nothing is fetched while building. It opens in an Edge or Chrome app window;
Edge is already part of Windows.

1. **Install Go.** Download the Windows installer (the `.msi`, 64-bit) from https://go.dev/dl/,
   run it and accept the defaults. Close any Command Prompt that was already open.
2. **Check Go works.** Press the Windows key, type `cmd`, press Enter, and run:

   ```
   go version
   ```

   It prints something like `go version go1.25.1 windows/amd64`. Go 1.19 or newer is fine.
3. **Get the source.** On the GitHub page press **Code**, then **Download ZIP**, and
   right-click the ZIP and choose **Extract All**. (With Git installed you can run
   `git clone` with the repository address instead.) Open the extracted folder until you see
   `main.go`, `go.mod` and `build.bat`.
4. **Set the version (optional).** Right-click `build.bat`, choose **Edit**, and change
   `vX.X.X` to the version you want shown at the bottom of the window, for example `v1.0.0`.
5. **Build.** Double-click `build.bat`. A black window shows the build; when it says
   `Built Recu.exe`, the new `Recu.exe` is in the same folder. The first build takes a minute.
   `build.bat` contains:

   ```
   go build -trimpath -ldflags="-s -w -X main.tag=vX.X.X" -buildvcs=false -o Recu.exe
   ```

   `-trimpath` leaves your folder names out of the file, `-buildvcs=false` leaves out Git
   details, and `-s -w` makes it smaller. To stop the black console window from opening next to
   the program, add `-H=windowsgui` inside the quotes: `-ldflags="-H=windowsgui -s -w -X main.tag=vX.X.X"`.
6. **Run it.** Double-click `Recu.exe` and fill in the User-Agent and Cookie in Settings.

### Check that Recu.exe does not show where it was built

A file built without `-trimpath` has the full path of the folder it was built in stored inside
it, for example your user name. This check searches `Recu.exe` for the file name `main.go` and
prints the text around it.

1. Open the folder with `Recu.exe` in File Explorer, click the address bar, type `powershell`
   and press Enter.
2. Paste these three lines and press Enter:

   ```
   $b = [IO.File]::ReadAllBytes("$PWD\Recu.exe")
   $t = [Text.Encoding]::GetEncoding(28591).GetString($b)
   [regex]::Matches($t, '[ -~]{0,200}main\.go') | ForEach-Object Value | Sort-Object -Unique
   ```

3. Read the result:
   - **Good:** one line, `Recurbate/main.go`. That is the module name from `go.mod`, not a
     folder on your PC.
   - **Not good:** a line with a drive letter or a folder path, such as
     `C:/Users/<your name>/Downloads/Recurbate/main.go`. The file was built without
     `-trimpath`. Build it again with `build.bat` and run the check again.

## Changing the icons

There are two icons, and each is changed in its own way.

**The program icon** (what `Recu.exe` looks like in File Explorer and on the taskbar shortcut):

1. Make a square PNG, 256 × 256 pixels is good, and save it as `winres\icon.png`, replacing
   the old one. Keep the name and the folder.
2. Install the icon tool once. In Command Prompt run:

   ```
   go install github.com/tc-hib/go-winres@latest
   ```

   This puts `go-winres.exe` in `%USERPROFILE%\go\bin`. If the next step says
   `'go-winres' is not recognized`, add that folder to your PATH: press the Windows key, type
   `environment variables`, open **Edit environment variables for your account**, select
   **Path**, press **Edit**, **New**, paste `%USERPROFILE%\go\bin`, press OK three times, and
   open a new Command Prompt.
3. Double-click `change-icon.bat`. It runs `go-winres make`, which turns `winres\icon.png`
   into `.syso` files next to `main.go`. Go picks those up by itself.
4. Run `build.bat` again. The new `Recu.exe` has the new icon. If Explorer still shows the old
   one, copy `Recu.exe` to another folder or restart Explorer; Windows remembers icons.

Do steps 2 and 3 once even if you keep the original picture: until `change-icon.bat` has
run, `Recu.exe` has the plain default icon.

**The window and taskbar icon** (the picture on the open window): replace
`server\web\icon.png` with your own square PNG and run `build.bat` again. No tool is needed.

## Tests

`go test ./...` runs the tests. They use a local mock of the site, so they need no internet.
