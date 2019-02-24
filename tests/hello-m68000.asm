*
*       Program to repeatedly display "Hello, World!" on the
*       Motorola 68000
*
*       Written by Stephane Brunet, Computer Engineering student
*       at Concordia University, Montreal, Canada.
*
*       s_brunet@ece.concordia.ca
*

        org     $1000
        ;Main function: use a jump to this address from debugger...
main
        move.l  #str,a0         ;load A0 register with address of string
        movem.l a0,-(sp)        ;push address of string on stack
        bsr     _puts           ;branch to subroutine "_puts"
        bra     main            ;keep looping!


        org     $2000
str     dc.b    'Hello, World!',10,0

        org     $3000
******
_puts   ;Like C/C++ puts function (LF added)
******  ;returns nothing

    ;save regs
    movem.l d0-d1/d7/a0/a5/a6,-(sp)

    move.l  28(sp),a5   ;get address of string from stack

    ;find end of string
    move.l  a5,a6
1$  move.b  (a6)+,d0    ;get next char of string
    cmp.b   #0,d0       ;is it a null?
    beq 2$      ;   yes, found end of string
    jmp 1$      ;   no, so keep looping

2$  subq    #1,a6           ;don't print the null
    move.w  #227,d7         ;call out1cr trap
    trap    #14

    ;retore regs & return
    movem.l (sp)+,d0-d1/d7/a0/a5/a6
    rts
;end _puts

        END
