comment Replace "web" with "rebol" in all files in the current directory
foreach file read %./ [
    if find [%.txt %.text] suffix? file [
	text: read file
	replace/all text "web" "rebol"
	write file text
    ]
]
