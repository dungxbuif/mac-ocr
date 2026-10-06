# Native worker UI contract

The distributed app is a menu-bar accessory. Clicking its OCR status icon opens
the established compact card. It shows the worker state, a short status message,
capacity, an on/off switch, log access, Launch at Login, and quit. The switch has
an accessibility label. The card does not show endpoint, Connection/configuration
controls, credentials, operating mode, node identity/name, enrollment, machine
metadata, or release engineering notes.

The worker auto-connects using its baked production configuration. The switch
remains interactive while connecting, so OFF cancels retries. It is disabled
only during stopping while accepted jobs drain. Node identity, machine metadata,
routing and administrative controls remain exclusive to the admin dashboard.
