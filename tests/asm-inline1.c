// From https://en.wikipedia.org/wiki/Inline_assembler
extern int errno;

int funcname(int arg1, int *arg2, int arg3)
{
  int res;
  __asm__ volatile (
    "int $0x80"        /* make the request to the OS */
    : "=a" (res),      /* return result in eax ("a") */
      "+b" (arg1),     /* pass arg1 in ebx ("b") */
      "+c" (arg2),     /* pass arg2 in ecx ("c") */
      "+d" (arg3)      /* pass arg3 in edx ("d") */
    : "a"  (128)       /* pass system call number in eax ("a") */
    : "memory", "cc"); /* announce to the compiler that the memory and condition codes have been modified */

  /* The operating system will return a negative value on error;
   * wrappers return -1 on error and set the errno global variable */
  if (-125 <= res && res < 0) {
    errno = -res;
    res   = -1;
  }  
  return res;
}
