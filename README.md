# Recurbate GUI Downloader

## Using it

* Run `Recu.exe`. A window opens (Edge or Chrome app window).
* Open **Settings** (gear, top right) and fill in **User-Agent** and **Cookie**. Nothing
   downloads until both are set.
---

<div align="center"><img width="-1" height="200" alt="image" src="https://github.com/user-attachments/assets/83a14d71-d6e1-461f-9e2a-cbdead45bada" />
<img width="-1" height="200" alt="image" src="https://github.com/user-attachments/assets/e093be57-3389-44b4-8aee-e039bc7492eb" /> </div>

---
### Quick Pasting Cookies
   When the site gives you a new cookie, copy it in your browser and press the key button
   (**Replace cookie**) at the top right of the main window, next to the clipboard button.

   This will pause all running downloads and will have to be started manually again.

<div align="center"><img width="-1" height="200" alt="image" src="https://github.com/user-attachments/assets/ff27b16d-7b2b-4aaa-a1cb-4066df71df54" /></div>

---

* Paste links in the box at the top, one per line, and press Add to list :
   
  <div align="center"> <img width="1000" height="-1" alt="image" src="https://github.com/user-attachments/assets/483f0c29-2861-481c-a610-781677e0164c" /></div>
   
   ---
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

## Several parts of one video

Rows with the same link and different start and end times
download to the same file name. They are numbered in the order they have in the list, whichever
starts first: the top row is `name.ts`, the next `name_01.ts`, then `name_02.ts` and so on up to
`name_99.ts`

---

## Preload Files 
Settings → Downloads → **Preload Files** (on by default). 
Each URL has its first two segments downloaded to Pre-assign the video to the Unfinished Folder

---

## Unique Server Find

(Settings → Experimental, off by default). 
Forces servers for each URL to be different from the one above it. 

**Could Speed Up Download Times, if you split a single Video into multiple Segments**

* The program is allowed to Retry a set amount of times (10 by default)
* Time Between Retries sets the wait time between retries (1 second by default, 0.1 at the least)
* Finished rows, and rows whose server is not known yet or is only remembered from an earlier session, are not taken into account
* If the setting is turned on it loads one URL on the table at a time, instead of two URLs at once

<div align="center"><img width="-1" height="300" alt="image" src="https://github.com/user-attachments/assets/a265f139-b729-45bb-87bb-23457934787a" /></div>

## Long Lasting Site (Settings → Experimental, on by default). 

<div align="center"><img width="300" height="-1" alt="image" src="https://github.com/user-attachments/assets/d498811f-b4dd-430a-adcf-c1378ebaec52" /></div>

**Ensure Setting is Enabled**
* When Downloads are started the files are read as they're started using the fresh cookies
* If turned off the Downloads further down the queue will fail due to a Site Refusal Error as it only reads the cookies (Now Expired) at the time of download

## Link listener

<div align="center"><img width="-1" height="200" alt="image" src="https://github.com/user-attachments/assets/3fea38d7-7be6-4036-ab79-a08bb96f400d" /></div>

Switch it on with the clipboard button at the top right of the main window 
* While it is on, each time you copy something, the downloader looks for links in it and puts each one on its own line in the link box on the main window (ready for *Add to list*)
* **Skip links that are already in the file** (on by default).
* **Also read copies from private windows** (on by default): browsers mark everything copied in a private or incognito window as private, so if links copied never arrive and you browse privately, enable this.
* **Try the patterns**: paste some text and press *Test patterns* to see what would go into the link
  box and into the text file, without saving anything.

If the URL ever changes you can change how the program picks up the URLs

<div align="center"><img width="-1" height="300" alt="image" src="https://github.com/user-attachments/assets/778be5ed-233e-477a-a8a6-58a6c951801f" /></div>

## Categories

Lets you create Categories in the settings, the feature allows you to:
* Set Custom Directories for the Finished, Unfinished and M3U8 Files
* Add Categories to URLs before they are added to the download table
* Change Categories by Right Clicking URLs already on the download table !This will reset the Progress of the URL Download!

## Modes

* **Normal**: every active row at the same time (optional cap in Settings).
* **Hybrid**: one row per server, different servers at the same time, top to bottom.
* **Series**: one row at a time, top to bottom.

## Folders and files

By default everything is kept in the folder the program is in, with no subfolders: videos
still downloading, finished videos, playlists saved with the Playlist button, and
`settings.json`, `queue.json` and `Recurbate.log`. A video keeps the same file while it
downloads and when it finishes (it just stops growing).

In Settings every folder box is blank by default (blank = the program's folder). A category
that leaves a folder blank uses the normal folder, so it ends up there too.
Type a full path such as `D:\Videos` or press **Browse…** to use another folder, on any drive.

### Program data folder (Settings) 
Recommended to Put the EXE in a set Folder and create a shortcut for the Program in another folder for a Cleaner Setup

<div align="center"><img width="-1" height="300" alt="image" src="https://github.com/user-attachments/assets/a6df074a-488a-4178-89f1-8e6445b8a649" /></div>

## Closing

Closing the window quits the program: running downloads are paused and saved, and
continue the next time you start it.

The power button under the gear does the same on demand: it asks first if downloads are
running or waiting, pauses and saves them, then closes the window and the program.

**I'd recommend to Pause All then Press the Power Button**

<div align="center"><img width="-1" height="300" alt="image" src="https://github.com/user-attachments/assets/93fa1898-443c-47ae-87ec-0df0df23cda0" /></div>

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
   `v3.X.X` to the version you want shown at the bottom of the window, for example `v3.1.4`.
5. **Build.** Double-click `build.bat`. A black window shows the build; when it says
   `Built Recu.exe`, the new `Recu.exe` is in the same folder. The first build takes a minute.
   `build.bat` contains:

   ```
   go build -trimpath -ldflags="-H=windowsgui -s -w -X main.tag=v3.X.X" -buildvcs=false -o Recu.exe
   ```

   `-H=windowsgui` stops a black console window from opening next to the program,
   `-trimpath` leaves your folder names out of the file, `-buildvcs=false` leaves out Git
   details, and `-s -w` makes it smaller.

## Check that Recu.exe does not show where it was built

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
