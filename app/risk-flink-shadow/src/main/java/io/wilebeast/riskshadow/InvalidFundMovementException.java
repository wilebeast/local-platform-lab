package io.wilebeast.riskshadow;

public final class InvalidFundMovementException extends RuntimeException {
    public InvalidFundMovementException(String message) {
        super(message);
    }

    public InvalidFundMovementException(String message, Throwable cause) {
        super(message, cause);
    }
}
