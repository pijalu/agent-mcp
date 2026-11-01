#include <stdio.h>

int main(void) {
    int guess = 0;

    printf("Guess the number: ");
    fflush(stdout);
    if (scanf("%d", &guess) != 1) {
        printf("NO\n");
        return 1;
    }

    if (guess == 42) {
        printf("YES\n");
    } else {
        printf("NO\n");
    }

    return 0;
}
