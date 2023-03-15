# slackforce

A Slack and Salesforce integration project written in Golang.

## How to get Slack Token and Cookie

TOKEN
+++++

#. Open your browser's *Developer Console*.

   #. In Firefox, under `Tools -> Browser Tools -> Web Developer tools` in the menu bar
   #. In Chrome, click the 'three dots' button to the right of the URL Bar, then select
      'More Tools -> Developer Tools'
#. Switch to the console tab.
#. Paste the following snippet and press ENTER to execute::

     JSON.parse(localStorage.localConfig_v2).teams[document.location.pathname.match(/^\/client\/(T[A-Z0-9]+)/)[1]].token

#. Token value is printed right after the executed command (it starts with
   "``xoxc-``"), save it somewhere for now.

COOKIE
++++++

**Getting the cookie value**

#. Switch to Application_ tab and select **Cookies** in the left
   navigation pane.
#. Find the cookie with the name "``d``".  That's right, just the
   letter "d".
#. Double-click the Value of this cookie.
#. Press Ctrl+C or Cmd+C to copy it's value to clipboard.
#. Save it for later.

Setting up the application
~~~~~~~~~~~~~~~~~~~~~~~~~~

#. Create the file named ``.env``.
#. Add the token and cookie values to it. End result
   should look like this::

     SLACK_TOKEN=xoxc-<...elided...>
     SLACK_COOKIE=xoxd-<...elided...>

   Alternatively, if you saved the cookies to the file, it will look like this::

     SLACK_TOKEN=xoxc-<...elided...>
     SLACK_COOKIE=path/to/slack.com_cookies.txt

#. Save the file and close the editor.

## Usage

```
make start
```